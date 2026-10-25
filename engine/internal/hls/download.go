package hls

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/amminvention/vidhawk/internal/fetch"
	"github.com/amminvention/vidhawk/internal/ffmpeg"
	"github.com/amminvention/vidhawk/internal/naming"
	"github.com/amminvention/vidhawk/internal/segdl"
)

// ErrDRM is returned for SAMPLE-AES / FairPlay / Widevine protected streams.
var ErrDRM = errors.New("stream is DRM-protected (SAMPLE-AES or key system); cannot be downloaded")

type Options struct {
	Quality     string
	Concurrency int
	FFmpeg      string // empty → no remux
	TmpRoot     string // empty → <outDir>/.vidhawk-tmp
}

type Result struct {
	Path  string
	Label string
}

// ProgressFunc receives segment counts plus cumulative bytes.
type ProgressFunc func(done, total int, bytes int64)

// Download fetches a master or media playlist URL into outDir as "<title> [label].mp4".
func Download(ctx context.Context, c *fetch.Client, playlistURL, outDir, title string, opt Options, progress ProgressFunc) (Result, error) {
	text, resp, err := c.GetText(ctx, playlistURL, nil, 16<<20)
	if err != nil {
		return Result{}, err
	}
	base := resp.Request.URL

	var media, audio *MediaPlaylist
	label := ""
	if IsMaster(text) {
		variants, renditions, err := ParseMaster(text, base)
		if err != nil {
			return Result{}, err
		}
		v := SelectVariant(variants, opt.Quality)
		label = v.Label()
		mtext, mresp, err := c.GetText(ctx, v.URL, nil, 64<<20)
		if err != nil {
			return Result{}, err
		}
		if media, err = ParseMedia(mtext, mresp.Request.URL); err != nil {
			return Result{}, err
		}
		if v.AudioGroup != "" {
			if r := PickAudio(renditions, v.AudioGroup); r != nil {
				atext, aresp, err := c.GetText(ctx, r.URL, nil, 64<<20)
				if err != nil {
					return Result{}, err
				}
				if audio, err = ParseMedia(atext, aresp.Request.URL); err != nil {
					return Result{}, err
				}
			}
		}
	} else if media, err = ParseMedia(text, base); err != nil {
		return Result{}, err
	}
	if len(media.Segments) == 0 {
		return Result{}, errors.New("hls: playlist has no segments")
	}
	if media.DRM() || (audio != nil && audio.DRM()) {
		return Result{}, ErrDRM
	}

	total := len(media.Segments)
	if audio != nil {
		total += len(audio.Segments)
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

	vpath, err := downloadPlaylist(ctx, c, media, filepath.Join(tmp, "video"), opt.Concurrency, onPart)
	if err != nil {
		return Result{}, err
	}
	apath := ""
	if audio != nil {
		if apath, err = downloadPlaylist(ctx, c, audio, filepath.Join(tmp, "audio"), opt.Concurrency, onPart); err != nil {
			return Result{}, err
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Result{}, err
	}
	final := naming.UniquePath(outDir, naming.MediaName(title, "mp4", label))
	if opt.FFmpeg != "" {
		inputs := []string{vpath}
		if apath != "" {
			inputs = append(inputs, apath)
		}
		if err := ffmpeg.Remux(ctx, opt.FFmpeg, final, inputs...); err != nil {
			return Result{}, err
		}
		return Result{Path: final, Label: label}, nil
	}
	// No ffmpeg: keep the raw container (concatenated TS or fMP4 is playable).
	ext := ".ts"
	if media.FMP4 {
		ext = ".mp4"
	}
	final = strings.TrimSuffix(final, ".mp4") + ext
	if err := moveFile(vpath, final); err != nil {
		return Result{}, err
	}
	if apath != "" {
		aext := ".aac"
		if audio.FMP4 {
			aext = ".m4a"
		}
		_ = moveFile(apath, strings.TrimSuffix(final, ext)+".audio"+aext)
	}
	return Result{Path: final, Label: label}, nil
}

func tempDir(root, outDir string) (string, error) {
	if root == "" {
		root = filepath.Join(outDir, ".vidhawk-tmp")
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	dir := filepath.Join(root, "hls-"+hex.EncodeToString(rnd[:]))
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

type keyCache struct {
	c  *fetch.Client
	mu sync.Mutex
	m  map[string][]byte
}

func (k *keyCache) get(ctx context.Context, uri string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if b, ok := k.m[uri]; ok {
		return b, nil
	}
	b, _, err := k.c.GetBytes(ctx, uri, nil, 1<<16)
	if err != nil {
		return nil, fmt.Errorf("key %s: %w", uri, err)
	}
	if len(b) != 16 {
		return nil, fmt.Errorf("key %s: expected 16 bytes, got %d", uri, len(b))
	}
	k.m[uri] = b
	return b, nil
}

type initCache struct {
	c  *fetch.Client
	mu sync.Mutex
	m  map[string][]byte
}

func (ic *initCache) get(ctx context.Context, u string, br *ByteRange) ([]byte, error) {
	id := u
	if br != nil {
		id = fmt.Sprintf("%s#%d@%d", u, br.Length, br.Offset)
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if b, ok := ic.m[id]; ok {
		return b, nil
	}
	b, _, err := ic.c.GetBytes(ctx, u, rangeHeader(br), 0)
	if err != nil {
		return nil, fmt.Errorf("init segment: %w", err)
	}
	ic.m[id] = b
	return b, nil
}

func rangeHeader(br *ByteRange) map[string]string {
	if br == nil {
		return nil
	}
	return map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", br.Offset, br.Offset+br.Length-1)}
}

func downloadPlaylist(ctx context.Context, c *fetch.Client, pl *MediaPlaylist, prefix string, concurrency int, onPart func(int)) (string, error) {
	ext := ".ts"
	if pl.FMP4 {
		ext = ".mp4"
	}
	out := prefix + ext
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()

	keys := &keyCache{c: c, m: map[string][]byte{}}
	inits := &initCache{c: c, m: map[string][]byte{}}
	fetcher := func(ctx context.Context, i int) ([]byte, error) {
		seg := pl.Segments[i]
		data, err := fetchSegment(ctx, c, seg, keys)
		if err != nil {
			return nil, err
		}
		if seg.MapURL != "" && (i == 0 || pl.Segments[i-1].MapURL != seg.MapURL) {
			init, err := inits.get(ctx, seg.MapURL, seg.MapRange)
			if err != nil {
				return nil, err
			}
			data = append(append([]byte(nil), init...), data...)
		}
		return data, nil
	}
	if err := segdl.Download(ctx, len(pl.Segments), concurrency, fetcher, f, prefix+"_parts", onPart); err != nil {
		return "", err
	}
	return out, nil
}

func fetchSegment(ctx context.Context, c *fetch.Client, seg Segment, keys *keyCache) ([]byte, error) {
	data, _, err := c.GetBytes(ctx, seg.URL, rangeHeader(seg.Range), 0)
	if err != nil {
		return nil, err
	}
	if seg.Key != nil && seg.Key.Method == "AES-128" {
		key, err := keys.get(ctx, seg.Key.URI)
		if err != nil {
			return nil, err
		}
		iv := seg.Key.IV
		if iv == nil {
			iv = make([]byte, 16)
			binary.BigEndian.PutUint64(iv[8:], uint64(seg.Seq))
		}
		if data, err = decryptCBC(data, key, iv); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func decryptCBC(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, errors.New("encrypted segment length is not a multiple of 16")
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	if n := len(out); n > 0 {
		if pad := int(out[n-1]); pad >= 1 && pad <= aes.BlockSize && pad <= n {
			out = out[:n-pad]
		}
	}
	return out, nil
}
