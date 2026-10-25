package dash

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/amminvention/vidhawk/internal/fetch"
	"github.com/amminvention/vidhawk/internal/ffmpeg"
	"github.com/amminvention/vidhawk/internal/naming"
	"github.com/amminvention/vidhawk/internal/segdl"
)

type Options struct {
	Quality     string
	Concurrency int
	FFmpeg      string
	TmpRoot     string
	OnEstimate  func(bytes int64)
}

type Result struct {
	Path  string
	Label string
}

type ProgressFunc func(done, total int, bytes int64)

// Download fetches the best video + audio representations and muxes them.
func Download(ctx context.Context, c *fetch.Client, mpdURL, outDir, title string, opt Options, progress ProgressFunc) (Result, error) {
	text, resp, err := c.GetText(ctx, mpdURL, nil, 32<<20)
	if err != nil {
		return Result{}, err
	}
	reps, err := ParseMPD(text, resp.Request.URL)
	if err != nil {
		return Result{}, err
	}
	videos, audios := Videos(reps), Audios(reps)
	var video, audio *Rep
	if len(videos) > 0 {
		v := SelectVideo(videos, opt.Quality)
		video = &v
	}
	if len(audios) > 0 {
		a := audios[0]
		audio = &a
	}
	if video == nil && audio == nil {
		return Result{}, errors.New("dash: nothing to download")
	}

	total := 0
	label := ""
	var estimate float64
	if video != nil {
		total += video.Units()
		label = video.Label()
		estimate += float64(video.Bandwidth) * video.Duration / 8
	}
	if audio != nil {
		total += audio.Units()
		estimate += float64(audio.Bandwidth) * audio.Duration / 8
	}
	if opt.OnEstimate != nil && estimate > 0 {
		opt.OnEstimate(int64(estimate))
	}
	tmp, err := tempDir(opt.TmpRoot, outDir)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)

	var mu sync.Mutex
	var done int
	var bytes int64
	onPart := func(n int) {
		mu.Lock()
		done++
		bytes += int64(n)
		d, b := done, bytes
		mu.Unlock()
		if progress != nil {
			progress(d, total, b)
		}
	}
	if progress != nil {
		progress(0, total, 0)
	}

	var vpath, apath string
	if video != nil {
		if vpath, err = downloadRep(ctx, c, video, filepath.Join(tmp, "video.m4s"), opt.Concurrency, onPart); err != nil {
			return Result{}, err
		}
	}
	if audio != nil {
		if apath, err = downloadRep(ctx, c, audio, filepath.Join(tmp, "audio.m4s"), opt.Concurrency, onPart); err != nil {
			return Result{}, err
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Result{}, err
	}
	ext := "mp4"
	if video == nil {
		ext = "m4a"
	}
	final := naming.UniquePath(outDir, naming.MediaName(title, ext, label))
	if opt.FFmpeg != "" {
		var inputs []string
		for _, p := range []string{vpath, apath} {
			if p != "" {
				inputs = append(inputs, p)
			}
		}
		if err := ffmpeg.Remux(ctx, opt.FFmpeg, final, inputs...); err != nil {
			return Result{}, err
		}
		return Result{Path: final, Label: label}, nil
	}
	// Without ffmpeg the concatenated fMP4 tracks are still valid files.
	if vpath != "" {
		if err := moveFile(vpath, final); err != nil {
			return Result{}, err
		}
		if apath != "" {
			_ = moveFile(apath, strings.TrimSuffix(final, "."+ext)+".audio.m4a")
		}
		return Result{Path: final, Label: label}, nil
	}
	if err := moveFile(apath, final); err != nil {
		return Result{}, err
	}
	return Result{Path: final, Label: label}, nil
}

func downloadRep(ctx context.Context, c *fetch.Client, rep *Rep, out string, concurrency int, onPart func(int)) (string, error) {
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if rep.SingleURL != "" {
		resp, err := c.Get(ctx, rep.SingleURL, map[string]string{"Accept-Encoding": "identity"})
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		n, err := io.Copy(f, resp.Body)
		if err != nil {
			return "", err
		}
		onPart(int(n))
		return out, nil
	}
	if rep.InitURL != "" {
		init, _, err := c.GetBytes(ctx, rep.InitURL, nil, 0)
		if err != nil {
			return "", err
		}
		if _, err := f.Write(init); err != nil {
			return "", err
		}
		onPart(len(init))
	}
	fetcher := func(ctx context.Context, i int) ([]byte, error) {
		b, _, err := c.GetBytes(ctx, rep.Segments[i], nil, 0)
		return b, err
	}
	if err := segdl.Download(ctx, len(rep.Segments), concurrency, fetcher, f, out+"_parts", onPart); err != nil {
		return "", err
	}
	return out, nil
}

func tempDir(root, outDir string) (string, error) {
	if root == "" {
		root = filepath.Join(outDir, ".vidhawk-tmp")
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	dir := filepath.Join(root, "dash-"+hex.EncodeToString(rnd[:]))
	return dir, os.MkdirAll(dir, 0o755)
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	return os.Remove(src)
}
