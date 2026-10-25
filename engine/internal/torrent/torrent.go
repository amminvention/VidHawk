// Package torrent downloads magnets and .torrent files with anacrolix/torrent.
package torrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	analog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

type Options struct {
	DataDir    string
	ListenPort int
	Seed       bool
}

// Progress reports bytes, download rate (B/s), peer count and torrent name.
type Progress func(done, total int64, rate float64, peers int, name string)

// FetchFunc downloads an http(s) .torrent file for us (with browser headers).
type FetchFunc func(ctx context.Context, url string) ([]byte, error)

// Download runs until the torrent is complete and returns the content path.
func Download(ctx context.Context, src string, fetchBytes FetchFunc, opt Options, progress Progress) (string, error) {
	if opt.DataDir == "" {
		return "", errors.New("torrent: data dir is required")
	}
	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return "", err
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = opt.DataDir
	cfg.Seed = opt.Seed
	if opt.ListenPort > 0 {
		cfg.ListenPort = opt.ListenPort
	}
	cfg.Logger = analog.Default.FilterLevel(analog.Error)
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return "", fmt.Errorf("torrent client: %w", err)
	}
	defer cl.Close()

	var t *torrent.Torrent
	switch {
	case strings.HasPrefix(strings.ToLower(src), "magnet:"):
		t, err = cl.AddMagnet(src)
	case strings.HasPrefix(strings.ToLower(src), "http://"), strings.HasPrefix(strings.ToLower(src), "https://"):
		data, ferr := fetchBytes(ctx, src)
		if ferr != nil {
			return "", ferr
		}
		mi, merr := metainfo.Load(bytes.NewReader(data))
		if merr != nil {
			return "", fmt.Errorf("invalid .torrent: %w", merr)
		}
		t, err = cl.AddTorrent(mi)
	default:
		t, err = cl.AddTorrentFromFile(src)
	}
	if err != nil {
		return "", fmt.Errorf("add torrent: %w", err)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.GotInfo():
			goto gotInfo
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if progress != nil {
				progress(0, 0, 0, t.Stats().TotalPeers, t.Name())
			}
		}
	}
gotInfo:
	t.DownloadAll()
	var lastDone int64
	lastT := time.Now()
	for {
		done, total := t.BytesCompleted(), t.Length()
		now := time.Now()
		rate := float64(done-lastDone) / now.Sub(lastT).Seconds()
		lastDone, lastT = done, now
		if progress != nil {
			progress(done, total, rate, t.Stats().ActivePeers, t.Name())
		}
		if total > 0 && done >= total {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
	return filepath.Join(opt.DataDir, t.Name()), nil
}
