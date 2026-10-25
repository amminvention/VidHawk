// Package httpdl downloads a single HTTP resource with many parallel ranged
// connections and resumable state (<file>.vidhawk).
package httpdl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amminvention/vidhawk/internal/fetch"
	"github.com/amminvention/vidhawk/internal/naming"
)

const (
	DefaultMinChunk = 1 << 20
	chunkAttempts   = 8
	stateInterval   = time.Second
)

type Info struct {
	Size         int64  `json:"size"`
	Ranges       bool   `json:"acceptRanges"`
	Filename     string `json:"filename,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	FinalURL     string `json:"finalUrl"`
	ETag         string `json:"-"`
	LastModified string `json:"-"`
}

// Probe asks for the first byte to learn size, range support and file name
// without downloading the body.
func Probe(ctx context.Context, c *fetch.Client, rawURL string) (Info, error) {
	resp, err := c.Get(ctx, rawURL, map[string]string{"Range": "bytes=0-0", "Accept-Encoding": "identity"})
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	info := Info{
		FinalURL:     resp.Request.URL.String(),
		ContentType:  resp.Header.Get("Content-Type"),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Filename:     naming.ContentDispositionFilename(resp.Header.Get("Content-Disposition")),
	}
	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				if n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
					info.Size = n
					info.Ranges = true
				}
			}
		}
		return info, nil
	}
	if resp.ContentLength > 0 {
		info.Size = resp.ContentLength
	}
	info.Ranges = strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes")
	return info, nil
}

type Options struct {
	Connections int
	MinChunk    int64
}

type ProgressFunc func(done, total int64)

type chunk struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Done  int64 `json:"done"`
}

type state struct {
	URL    string   `json:"url"`
	Size   int64    `json:"size"`
	ETag   string   `json:"etag,omitempty"`
	Chunks []*chunk `json:"chunks"`
}

func statePath(dest string) string { return dest + ".vidhawk" }

// Download writes the resource described by info to dest, resuming a previous
// attempt when a matching state file exists.
func Download(ctx context.Context, c *fetch.Client, info Info, dest string, opt Options, progress ProgressFunc) error {
	if opt.Connections < 1 {
		opt.Connections = 1
	}
	if opt.MinChunk <= 0 {
		opt.MinChunk = DefaultMinChunk
	}
	if !info.Ranges || info.Size <= 2*opt.MinChunk || opt.Connections == 1 {
		return single(ctx, c, info, dest, progress)
	}
	st := loadState(dest, info)
	fresh := st == nil
	if fresh {
		n := int64(opt.Connections * 4)
		size := (info.Size + n - 1) / n
		if size < opt.MinChunk {
			size = opt.MinChunk
		}
		st = &state{URL: info.FinalURL, Size: info.Size, ETag: info.ETag}
		for start := int64(0); start < info.Size; start += size {
			end := start + size - 1
			if end >= info.Size {
				end = info.Size - 1
			}
			st.Chunks = append(st.Chunks, &chunk{Start: start, End: end})
		}
	}
	flags := os.O_RDWR
	if fresh {
		flags |= os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(dest, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if fresh {
		if err := f.Truncate(info.Size); err != nil {
			return err
		}
	}

	var done atomic.Int64
	for _, ch := range st.Chunks {
		done.Add(ch.Done)
	}
	var stateMu sync.Mutex
	lastSave := time.Now()
	save := func(force bool) {
		stateMu.Lock()
		defer stateMu.Unlock()
		if !force && time.Since(lastSave) < stateInterval {
			return
		}
		lastSave = time.Now()
		if data, err := json.Marshal(st); err == nil {
			_ = os.WriteFile(statePath(dest), data, 0o644)
		}
	}
	report := func() {
		if progress != nil {
			progress(done.Load(), info.Size)
		}
	}
	report()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan *chunk)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	fail := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}
	for i := 0; i < opt.Connections; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range work {
				if err := fetchChunk(ctx, c, info.FinalURL, f, ch, &done, func() { report(); save(false) }); err != nil {
					fail(err)
					return
				}
			}
		}()
	}
feed:
	for _, ch := range st.Chunks {
		if ch.Done >= ch.End-ch.Start+1 {
			continue
		}
		select {
		case work <- ch:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if firstErr != nil {
		save(true)
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		save(true)
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	os.Remove(statePath(dest))
	report()
	return nil
}

func fetchChunk(ctx context.Context, c *fetch.Client, url string, f *os.File, ch *chunk, done *atomic.Int64, tick func()) error {
	buf := make([]byte, 256<<10)
	var lastErr error
	for attempt := 0; attempt < chunkAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		remaining := ch.End - ch.Start + 1 - ch.Done
		if remaining <= 0 {
			return nil
		}
		start := ch.Start + ch.Done
		resp, err := c.Get(ctx, url, map[string]string{
			"Range":           fmt.Sprintf("bytes=%d-%d", start, ch.End),
			"Accept-Encoding": "identity",
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			return fmt.Errorf("server ignored Range request (HTTP %d)", resp.StatusCode)
		}
		var rerr error
		for remaining > 0 {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if int64(n) > remaining {
					n = int(remaining)
				}
				if _, werr := f.WriteAt(buf[:n], start); werr != nil {
					resp.Body.Close()
					return werr
				}
				start += int64(n)
				remaining -= int64(n)
				ch.Done += int64(n)
				done.Add(int64(n))
				tick()
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					rerr = err
				}
				break
			}
		}
		resp.Body.Close()
		if remaining <= 0 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if rerr == nil {
			rerr = io.ErrUnexpectedEOF
		}
		lastErr = rerr
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return fmt.Errorf("chunk %d-%d failed: %w", ch.Start, ch.End, lastErr)
}

func loadState(dest string, info Info) *state {
	data, err := os.ReadFile(statePath(dest))
	if err != nil {
		return nil
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil || st.Size != info.Size || len(st.Chunks) == 0 {
		return nil
	}
	if st.ETag != "" && info.ETag != "" && st.ETag != info.ETag {
		return nil
	}
	if fi, err := os.Stat(dest); err != nil || fi.Size() != info.Size {
		return nil
	}
	return &st
}

func single(ctx context.Context, c *fetch.Client, info Info, dest string, progress ProgressFunc) error {
	resp, err := c.Get(ctx, info.FinalURL, map[string]string{"Accept-Encoding": "identity"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	total := info.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	var done int64
	buf := make([]byte, 256<<10)
	last := time.Now()
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if progress != nil && time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				progress(done, total)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
	if progress != nil {
		progress(done, done)
	}
	return nil
}
