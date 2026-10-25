// Package api exposes the engine on 127.0.0.1 over REST + WebSocket for the
// desktop app, and provides a client that lets a second process (the browser
// native host) proxy to an already running engine.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/amminvention/vidhawk/internal/engine"
	"github.com/amminvention/vidhawk/internal/installer"
)

// Endpoint is written to <config dir>/api.json so clients can find the engine.
type Endpoint struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func EndpointPath(dir string) string { return filepath.Join(dir, "api.json") }

var ErrAlreadyRunning = errors.New("another VidHawk engine is already running")

type Server struct {
	be   engine.Backend
	tok  string
	ln   net.Listener
	srv  *http.Server
	dir  string
	Port int
}

// Start listens on an ephemeral localhost port unless another engine answers.
func Start(be engine.Backend, dir string, port int) (*Server, error) {
	if ep, err := ReadEndpoint(dir); err == nil && alive(ep) {
		return nil, ErrAlreadyRunning
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	var rnd [24]byte
	_, _ = rand.Read(rnd[:])
	s := &Server{be: be, tok: hex.EncodeToString(rnd[:]), ln: ln, dir: dir, Port: ln.Addr().(*net.TCPAddr).Port}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.Handle("GET /engine", s.auth(s.engineInfo))
	mux.Handle("GET /jobs", s.auth(s.listJobs))
	mux.Handle("POST /jobs", s.auth(s.createJob))
	mux.Handle("POST /jobs/{id}/{action}", s.auth(s.jobAction))
	mux.Handle("DELETE /jobs/{id}", s.auth(s.deleteJob))
	mux.Handle("POST /probe", s.auth(s.probe))
	mux.Handle("GET /config", s.auth(s.getConfig))
	mux.Handle("PUT /config", s.auth(s.putConfig))
	mux.Handle("POST /open", s.auth(s.open))
	mux.Handle("GET /ws", s.auth(s.ws))
	mux.Handle("GET /integration", s.auth(s.integration))
	mux.Handle("POST /integration", s.auth(s.installIntegration))
	s.srv = &http.Server{Handler: cors(mux), ReadHeaderTimeout: 10 * time.Second}
	ep := Endpoint{Port: s.Port, Token: s.tok, PID: os.Getpid()}
	data, _ := json.Marshal(ep)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(EndpointPath(dir), data, 0o600); err != nil {
		return nil, err
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (s *Server) Close() error {
	_ = os.Remove(EndpointPath(s.dir))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func ReadEndpoint(dir string) (Endpoint, error) {
	var ep Endpoint
	data, err := os.ReadFile(EndpointPath(dir))
	if err != nil {
		return ep, err
	}
	if err := json.Unmarshal(data, &ep); err != nil {
		return ep, err
	}
	if ep.Port == 0 || ep.Token == "" {
		return ep, errors.New("api: malformed endpoint file")
	}
	return ep, nil
}

func alive(ep Endpoint) bool {
	c := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/health", ep.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var h struct {
		OK  bool `json:"ok"`
		PID int  `json:"pid"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	return h.OK && h.PID == ep.PID
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.tok)) != 1 {
			writeErr(w, http.StatusUnauthorized, errors.New("bad token"))
			return
		}
		h(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "pid": os.Getpid(), "version": engine.Version})
}

func (s *Server) engineInfo(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.be.Info()) }
func (s *Server) listJobs(w http.ResponseWriter, _ *http.Request)   { writeJSON(w, 200, s.be.Jobs()) }
func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request)  { writeJSON(w, 200, s.be.Config()) }

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var spec engine.Spec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeErr(w, 400, err)
		return
	}
	job, err := s.be.Submit(spec)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, job)
}

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "cancel":
		err = s.be.Cancel(id)
	case "pause":
		err = s.be.Pause(id)
	case "resume":
		err = s.be.Resume(id)
	default:
		writeErr(w, 404, errors.New("unknown action"))
		return
	}
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	job, _ := s.be.Get(id)
	writeJSON(w, 200, job)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	deleteFile := r.URL.Query().Get("file") == "1" || r.URL.Query().Get("file") == "true"
	if err := s.be.Remove(r.PathValue("id"), deleteFile); err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) probe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	res, err := s.be.Probe(ctx, req.URL, req.Headers)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, 400, err)
		return
	}
	cfg, err := s.be.UpdateConfig(patch)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, cfg)
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.be.Open(req.Path); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) integration(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"browsers": installer.Installed(), "manifest": installer.ManifestPath(), "extensionId": installer.DefaultExtensionID})
}

func (s *Server) installIntegration(w http.ResponseWriter, _ *http.Request) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	done, err := installer.Install(exe, nil, nil)
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "installed": done})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "installed": done, "browsers": installer.Installed()})
}

// ws streams a hello (engine info + jobs) followed by job events.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	hello := map[string]any{"event": "hello", "engine": s.be.Info(), "jobs": s.be.Jobs(), "config": s.be.Config()}
	if err := wsjson.Write(ctx, c, hello); err != nil {
		return
	}
	events := make(chan engine.Event, 256)
	unsub := s.be.Subscribe(func(ev engine.Event) {
		select {
		case events <- ev:
		default:
		}
	})
	defer unsub()
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			if err := wsjson.Write(ctx, c, ev); err != nil {
				return
			}
		case <-ping.C:
			if err := c.Ping(ctx); err != nil {
				return
			}
		}
	}
}
