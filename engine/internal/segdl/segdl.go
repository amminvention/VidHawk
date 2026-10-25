// Package segdl downloads many small parts concurrently and writes them out in
// order. HLS and DASH both build on it.
package segdl

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Fetcher returns the bytes of part i.
type Fetcher func(ctx context.Context, i int) ([]byte, error)

func partPath(dir string, i int) string { return filepath.Join(dir, fmt.Sprintf("part%06d", i)) }

// Download fetches n parts with at most `concurrency` in flight, spools each
// to tmpDir, then appends them to out in index order and removes the spool.
func Download(ctx context.Context, n, concurrency int, fetch Fetcher, out io.Writer, tmpDir string, onPart func(bytes int)) error {
	if concurrency < 1 {
		concurrency = 1
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}

loop:
	for i := 0; i < n; i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			data, err := fetch(ctx, i)
			if err != nil {
				fail(fmt.Errorf("part %d: %w", i, err))
				return
			}
			if err := os.WriteFile(partPath(tmpDir, i), data, 0o644); err != nil {
				fail(err)
				return
			}
			if onPart != nil {
				onPart(len(data))
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		p := partPath(tmpDir, i)
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		f.Close()
		os.Remove(p)
		if err != nil {
			return err
		}
	}
	return nil
}
