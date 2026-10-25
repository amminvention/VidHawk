package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/amminvention/vidhawk/internal/config"
	"github.com/amminvention/vidhawk/internal/engine"
)

// Client implements engine.Backend against a running engine's local API.
type Client struct {
	base string
	tok  string
	http *http.Client

	mu   sync.Mutex
	subs map[int]func(engine.Event)
	next int
	wsUp bool
}

var _ engine.Backend = (*Client)(nil)

// Connect returns a client when an engine is reachable, else an error.
func Connect(dir string) (*Client, error) {
	ep, err := ReadEndpoint(dir)
	if err != nil {
		return nil, err
	}
	if !alive(ep) {
		return nil, errors.New("api: no running engine")
	}
	return &Client{base: fmt.Sprintf("http://127.0.0.1:%d", ep.Port), tok: ep.Token,
		http: &http.Client{Timeout: 60 * time.Second}, subs: map[int]func(engine.Event){}}, nil
}

func (c *Client) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) Info() engine.Info {
	var info engine.Info
	_ = c.do("GET", "/engine", nil, &info)
	return info
}

func (c *Client) Jobs() []engine.Job {
	var jobs []engine.Job
	_ = c.do("GET", "/jobs", nil, &jobs)
	return jobs
}

func (c *Client) Get(id string) (engine.Job, bool) {
	for _, j := range c.Jobs() {
		if j.ID == id {
			return j, true
		}
	}
	return engine.Job{}, false
}

func (c *Client) Submit(spec engine.Spec) (engine.Job, error) {
	var job engine.Job
	err := c.do("POST", "/jobs", spec, &job)
	return job, err
}

func (c *Client) Cancel(id string) error { return c.do("POST", "/jobs/"+id+"/cancel", nil, nil) }
func (c *Client) Pause(id string) error  { return c.do("POST", "/jobs/"+id+"/pause", nil, nil) }
func (c *Client) Resume(id string) error { return c.do("POST", "/jobs/"+id+"/resume", nil, nil) }
func (c *Client) Remove(id string) error { return c.do("DELETE", "/jobs/"+id, nil, nil) }

func (c *Client) Probe(_ context.Context, rawURL string, headers map[string]string) (*engine.ProbeResult, error) {
	var res engine.ProbeResult
	if err := c.do("POST", "/probe", map[string]any{"url": rawURL, "headers": headers}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) Config() config.Config {
	var cfg config.Config
	_ = c.do("GET", "/config", nil, &cfg)
	return cfg
}

func (c *Client) UpdateConfig(patch map[string]any) (config.Config, error) {
	var cfg config.Config
	err := c.do("PUT", "/config", patch, &cfg)
	return cfg, err
}

func (c *Client) Open(path string) error {
	return c.do("POST", "/open", map[string]any{"path": path}, nil)
}

func (c *Client) Active() int {
	n := 0
	for _, j := range c.Jobs() {
		if j.Active() {
			n++
		}
	}
	return n
}

// Subscribe opens the event WebSocket on first use and fans events out.
func (c *Client) Subscribe(fn func(engine.Event)) func() {
	c.mu.Lock()
	id := c.next
	c.next++
	c.subs[id] = fn
	start := !c.wsUp
	c.wsUp = true
	c.mu.Unlock()
	if start {
		go c.pump()
	}
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

func (c *Client) pump() {
	for {
		c.mu.Lock()
		n := len(c.subs)
		c.mu.Unlock()
		if n == 0 {
			c.mu.Lock()
			c.wsUp = false
			c.mu.Unlock()
			return
		}
		c.readLoop()
		time.Sleep(time.Second)
	}
}

func (c *Client) readLoop() {
	ctx := context.Background()
	url := "ws" + c.base[len("http"):] + "/ws?token=" + c.tok
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(8 << 20)
	for {
		var ev engine.Event
		if err := wsjson.Read(ctx, conn, &ev); err != nil {
			return
		}
		if ev.Type != "job" || ev.Job == nil {
			continue
		}
		c.mu.Lock()
		subs := make([]func(engine.Event), 0, len(c.subs))
		for _, fn := range c.subs {
			subs = append(subs, fn)
		}
		c.mu.Unlock()
		for _, fn := range subs {
			fn(ev)
		}
	}
}
