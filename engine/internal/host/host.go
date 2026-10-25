// Package host speaks Chrome native messaging over stdin/stdout on behalf of
// any engine.Backend (the in-process manager or a remote running engine).
package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"time"

	"github.com/amminvention/vidhawk/internal/engine"
	"github.com/amminvention/vidhawk/internal/protocol"
)

type reply map[string]any

// Run serves until stdin closes, then waits for active jobs to finish so a
// download survives the browser disconnecting from the host.
func Run(ctx context.Context, be engine.Backend, in io.Reader, out io.Writer, logger *log.Logger) error {
	w := protocol.NewWriter(out)
	unsub := be.Subscribe(func(ev engine.Event) {
		if err := w.Send(ev); err != nil {
			logger.Printf("send event: %v", err)
		}
	})
	defer unsub()

	for {
		raw, err := protocol.Read(in)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			logger.Printf("read: %v", err)
			break
		}
		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = w.Send(reply{"ok": false, "error": "invalid JSON: " + err.Error()})
			continue
		}
		if req.Cmd == "probe" {
			go func(req protocol.Request) { _ = w.Send(handle(ctx, be, req)) }(req)
			continue
		}
		if err := w.Send(handle(ctx, be, req)); err != nil {
			logger.Printf("send: %v", err)
			break
		}
	}
	logger.Printf("browser disconnected; %d active job(s)", be.Active())
	for be.Active() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}

func handle(ctx context.Context, be engine.Backend, req protocol.Request) reply {
	r := reply{"id": req.ID, "ok": true}
	fail := func(err error) reply {
		return reply{"id": req.ID, "ok": false, "error": err.Error()}
	}
	switch req.Cmd {
	case "ping":
		r["engine"] = be.Info()
		r["jobs"] = be.Jobs()
		r["config"] = be.Config()
	case "download":
		job, err := be.Submit(engine.Spec{URL: req.URL, Kind: req.Kind, Title: req.Title, Quality: req.Quality,
			PageURL: req.PageURL, OutDir: req.OutDir, Headers: req.Headers})
		if err != nil {
			return fail(err)
		}
		r["job"] = job
	case "probe":
		pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		res, err := be.Probe(pctx, req.URL, req.Headers)
		if err != nil {
			return fail(err)
		}
		r["probe"] = res
	case "jobs":
		r["jobs"] = be.Jobs()
	case "cancel":
		if err := be.Cancel(req.JobID); err != nil {
			return fail(err)
		}
	case "pause":
		if err := be.Pause(req.JobID); err != nil {
			return fail(err)
		}
	case "resume":
		if err := be.Resume(req.JobID); err != nil {
			return fail(err)
		}
	case "remove":
		if err := be.Remove(req.JobID); err != nil {
			return fail(err)
		}
	case "open":
		if err := be.Open(req.Path); err != nil {
			return fail(err)
		}
	case "config":
		if req.Set != nil {
			cfg, err := be.UpdateConfig(req.Set)
			if err != nil {
				return fail(err)
			}
			r["config"] = cfg
		} else {
			r["config"] = be.Config()
		}
	default:
		return fail(errors.New("unknown command: " + req.Cmd))
	}
	return r
}
