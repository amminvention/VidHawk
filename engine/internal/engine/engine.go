// Package engine is the job manager every VidHawk client talks to: it detects
// what a URL is, runs the right downloader, tracks progress and persists jobs.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amminvention/vidhawk/internal/config"
	"github.com/amminvention/vidhawk/internal/dash"
	"github.com/amminvention/vidhawk/internal/fetch"
	"github.com/amminvention/vidhawk/internal/ffmpeg"
	"github.com/amminvention/vidhawk/internal/hls"
	"github.com/amminvention/vidhawk/internal/httpdl"
	"github.com/amminvention/vidhawk/internal/naming"
	"github.com/amminvention/vidhawk/internal/proc"
	"github.com/amminvention/vidhawk/internal/resolver/ytdlp"
	"github.com/amminvention/vidhawk/internal/store"
	"github.com/amminvention/vidhawk/internal/torrent"
)

// Version is overridden at build time with -ldflags "-X ...engine.Version=".
var Version = "0.1.0-dev"

const (
	KindAuto    = "auto"
	KindFile    = "file"
	KindHLS     = "hls"
	KindDASH    = "dash"
	KindTorrent = "torrent"
	KindYtDlp   = "ytdlp"

	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusDone      = "done"
	StatusError     = "error"
	StatusCancelled = "cancelled"
)

// Job is the public job state shared by the extension, CLI and desktop app.
type Job struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Quality string  `json:"quality,omitempty"`
	PageURL string  `json:"pageUrl,omitempty"`
	OutDir  string  `json:"outDir"`
	Status  string  `json:"status"`
	Done    int64   `json:"done"`
	Total   int64   `json:"total"`
	Unit    string  `json:"unit"`
	Bytes   int64   `json:"bytes"`
	Speed   float64 `json:"speed"`
	Percent float64 `json:"percent"`
	Detail  string  `json:"detail,omitempty"`
	Peers   int     `json:"peers,omitempty"`
	Path    string  `json:"path,omitempty"`
	Error   string  `json:"error,omitempty"`
	Created int64   `json:"created"`
	Updated int64   `json:"updated"`

	ExpectedSize int64 `json:"expectedSize,omitempty"`
}

func (j Job) Active() bool { return j.Status == StatusQueued || j.Status == StatusRunning }

// Spec is a download request.
type Spec struct {
	URL     string            `json:"url"`
	Kind    string            `json:"kind,omitempty"`
	Title   string            `json:"title,omitempty"`
	Quality string            `json:"quality,omitempty"`
	PageURL string            `json:"pageUrl,omitempty"`
	OutDir  string            `json:"outDir,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// ExpectedSize is what the page announced (Content-Range/Length seen by the
	// browser); a much smaller answer means an expired or session-bound link.
	ExpectedSize int64 `json:"expectedSize,omitempty"`
}

type Event struct {
	Type string `json:"event"`
	Job  *Job   `json:"job,omitempty"`
}

type Info struct {
	Version     string `json:"version"`
	FFmpeg      bool   `json:"ffmpeg"`
	YtDlp       bool   `json:"ytdlp"`
	Torrent     bool   `json:"torrent"`
	DownloadDir string `json:"downloadDir"`
	PID         int    `json:"pid"`
}

// Backend is implemented by the in-process Manager and by the API client that
// proxies to another running engine, so the native host can use either.
type Backend interface {
	Info() Info
	Jobs() []Job
	Get(id string) (Job, bool)
	Submit(Spec) (Job, error)
	Cancel(id string) error
	Pause(id string) error
	Resume(id string) error
	Remove(id string) error
	Probe(ctx context.Context, rawURL string, headers map[string]string) (*ProbeResult, error)
	Config() config.Config
	UpdateConfig(patch map[string]any) (config.Config, error)
	Open(path string) error
	Subscribe(fn func(Event)) (unsubscribe func())
	Active() int
}

type task struct {
	mu        sync.Mutex
	job       Job
	headers   map[string]string
	cancel    context.CancelFunc
	lastBytes int64
	lastT     time.Time
	lastEmit  time.Time
}

func (t *task) snap() Job {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.job
}

type persisted struct {
	Job     Job               `json:"job"`
	Headers map[string]string `json:"headers,omitempty"`
}

type Manager struct {
	cfgMu sync.RWMutex
	cfg   config.Config
	store *store.Store

	mu    sync.Mutex
	tasks map[string]*task
	order []string

	subMu   sync.Mutex
	subs    map[int]func(Event)
	nextSub int

	sem chan struct{}
}

var _ Backend = (*Manager)(nil)

func New(cfg config.Config, st *store.Store) *Manager {
	m := &Manager{cfg: cfg, store: st, tasks: map[string]*task{}, subs: map[int]func(Event){}}
	m.sem = make(chan struct{}, cfg.MaxJobs)
	if st != nil {
		rows, _ := st.LoadJobs(500)
		for _, data := range rows {
			var p persisted
			if json.Unmarshal(data, &p) != nil || p.Job.ID == "" {
				continue
			}
			if p.Job.Active() {
				p.Job.Status = StatusPaused
				p.Job.Detail = "interrupted"
				p.Job.Speed = 0
			}
			m.tasks[p.Job.ID] = &task{job: p.Job, headers: p.Headers}
			m.order = append(m.order, p.Job.ID)
		}
	}
	return m
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *Manager) Config() config.Config {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.cfg
}

func (m *Manager) UpdateConfig(patch map[string]any) (config.Config, error) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	next, err := m.cfg.Merge(patch)
	if err != nil {
		return m.cfg, err
	}
	if err := config.Save(next); err != nil {
		return m.cfg, err
	}
	m.cfg = next
	return next, nil
}

func (m *Manager) Info() Info {
	cfg := m.Config()
	return Info{
		Version:     Version,
		FFmpeg:      ffmpeg.Find(cfg.FFmpegPath) != "",
		YtDlp:       ytdlp.Find(cfg.YtDlpPath) != "",
		Torrent:     true,
		DownloadDir: cfg.DownloadDir,
		PID:         os.Getpid(),
	}
}

func (m *Manager) Open(path string) error { return proc.Open(path) }

func (m *Manager) Subscribe(fn func(Event)) func() {
	m.subMu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = fn
	m.subMu.Unlock()
	return func() {
		m.subMu.Lock()
		delete(m.subs, id)
		m.subMu.Unlock()
	}
}

func (m *Manager) emit(t *task, force bool) {
	t.mu.Lock()
	if !force && time.Since(t.lastEmit) < 250*time.Millisecond {
		t.mu.Unlock()
		return
	}
	t.lastEmit = time.Now()
	job := t.job
	t.mu.Unlock()
	if force {
		m.persist(t)
	}
	m.subMu.Lock()
	subs := make([]func(Event), 0, len(m.subs))
	for _, fn := range m.subs {
		subs = append(subs, fn)
	}
	m.subMu.Unlock()
	for _, fn := range subs {
		fn(Event{Type: "job", Job: &job})
	}
}

func (m *Manager) persist(t *task) {
	if m.store == nil {
		return
	}
	t.mu.Lock()
	p := persisted{Job: t.job, Headers: t.headers}
	t.mu.Unlock()
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = m.store.SaveJob(p.Job.ID, p.Job.Status, p.Job.Created, p.Job.Updated, data)
}

func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.tasks[id].snap())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return Job{}, false
	}
	return t.snap(), true
}

func (m *Manager) Active() int {
	n := 0
	for _, j := range m.Jobs() {
		if j.Active() {
			n++
		}
	}
	return n
}

func (m *Manager) Submit(spec Spec) (Job, error) {
	u := strings.TrimSpace(spec.URL)
	if u == "" {
		return Job{}, errors.New("url is required")
	}
	kind := strings.ToLower(spec.Kind)
	switch kind {
	case "", KindAuto, KindFile, KindHLS, KindDASH, KindTorrent, KindYtDlp:
	default:
		return Job{}, fmt.Errorf("unknown kind %q", spec.Kind)
	}
	if kind == "" {
		kind = KindAuto
	}
	cfg := m.Config()
	now := time.Now().UnixMilli()
	outDir := spec.OutDir
	if outDir == "" {
		outDir = cfg.DownloadDir
	}
	quality := spec.Quality
	if quality == "" {
		quality = "best"
	}
	t := &task{
		job: Job{ID: newID(), Kind: kind, URL: u, Title: strings.TrimSpace(spec.Title), Quality: quality, PageURL: spec.PageURL,
			OutDir: outDir, Status: StatusQueued, Unit: "bytes", Created: now, Updated: now, ExpectedSize: spec.ExpectedSize},
		headers: fetch.Normalize(spec.Headers),
	}
	m.mu.Lock()
	m.tasks[t.job.ID] = t
	m.order = append(m.order, t.job.ID)
	m.mu.Unlock()
	m.emit(t, true)
	m.start(t)
	return t.snap(), nil
}

func (m *Manager) start(t *task) {
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
	go m.run(ctx, t)
}

func (m *Manager) run(ctx context.Context, t *task) {
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		m.finish(t, ctx.Err())
		return
	}
	defer func() { <-m.sem }()
	t.mu.Lock()
	t.job.Status = StatusRunning
	t.job.Error = ""
	t.job.Detail = ""
	t.lastT = time.Time{}
	t.lastBytes = 0
	t.mu.Unlock()
	m.emit(t, true)
	m.finish(t, m.execute(ctx, t))
}

func (m *Manager) finish(t *task, err error) {
	t.mu.Lock()
	t.job.Speed = 0
	t.job.Updated = time.Now().UnixMilli()
	switch {
	case err == nil:
		t.job.Status = StatusDone
		t.job.Percent = 100
		t.job.Detail = ""
	case errors.Is(err, context.Canceled):
		if t.job.Status != StatusPaused {
			t.job.Status = StatusCancelled
		}
	default:
		t.job.Status = StatusError
		t.job.Error = err.Error()
	}
	t.cancel = nil
	t.mu.Unlock()
	m.emit(t, true)
}

func (m *Manager) lookup(id string) (*task, error) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("job %s not found", id)
	}
	return t, nil
}

func (m *Manager) Cancel(id string) error {
	t, err := m.lookup(id)
	if err != nil {
		return err
	}
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) Pause(id string) error {
	t, err := m.lookup(id)
	if err != nil {
		return err
	}
	t.mu.Lock()
	cancel := t.cancel
	if cancel != nil {
		t.job.Status = StatusPaused
	}
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) Resume(id string) error {
	t, err := m.lookup(id)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.job.Active() {
		t.mu.Unlock()
		return nil
	}
	if t.job.Status == StatusDone {
		t.mu.Unlock()
		return errors.New("job already finished")
	}
	t.job.Status = StatusQueued
	t.job.Error = ""
	t.job.Detail = ""
	t.mu.Unlock()
	m.emit(t, true)
	m.start(t)
	return nil
}

func (m *Manager) Remove(id string) error {
	t, err := m.lookup(id)
	if err != nil {
		return err
	}
	_ = m.Cancel(id)
	m.mu.Lock()
	delete(m.tasks, id)
	for i, o := range m.order {
		if o == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	if m.store != nil {
		_ = m.store.DeleteJob(id)
	}
	m.subMu.Lock()
	subs := make([]func(Event), 0, len(m.subs))
	for _, fn := range m.subs {
		subs = append(subs, fn)
	}
	m.subMu.Unlock()
	removed := t.snap()
	removed.Status = "removed"
	for _, fn := range subs {
		fn(Event{Type: "job", Job: &removed})
	}
	return nil
}

func (m *Manager) client(headers map[string]string) *fetch.Client {
	cfg := m.Config()
	return fetch.New(headers, cfg.UserAgent, cfg.VerifyTLS)
}

// report updates progress counters and computes a smoothed speed.
func (m *Manager) report(t *task, done, total, bytes int64, unit string, extra func(*Job)) {
	now := time.Now()
	t.mu.Lock()
	t.job.Done, t.job.Total, t.job.Bytes, t.job.Unit = done, total, bytes, unit
	if total > 0 {
		t.job.Percent = math.Round(float64(done)/float64(total)*1000) / 10
	}
	if t.lastT.IsZero() {
		t.lastT, t.lastBytes = now, bytes
	} else if dt := now.Sub(t.lastT); dt >= 500*time.Millisecond {
		inst := float64(bytes-t.lastBytes) / dt.Seconds()
		if t.job.Speed == 0 {
			t.job.Speed = inst
		} else {
			t.job.Speed = 0.7*t.job.Speed + 0.3*inst
		}
		t.lastT, t.lastBytes = now, bytes
	}
	if extra != nil {
		extra(&t.job)
	}
	t.job.Updated = now.UnixMilli()
	t.mu.Unlock()
	m.emit(t, false)
}

func (m *Manager) execute(ctx context.Context, t *task) error {
	job := t.snap()
	if err := os.MkdirAll(job.OutDir, 0o755); err != nil {
		return err
	}
	kind := job.Kind
	if kind == KindAuto {
		k, err := m.detectKind(ctx, job, t.headers)
		if err != nil {
			return err
		}
		kind = k
		t.mu.Lock()
		t.job.Kind = kind
		t.mu.Unlock()
		m.emit(t, true)
	}
	switch kind {
	case KindFile:
		return m.runFile(ctx, t)
	case KindHLS:
		return m.runHLS(ctx, t)
	case KindDASH:
		return m.runDASH(ctx, t)
	case KindTorrent:
		return m.runTorrent(ctx, t)
	case KindYtDlp:
		return m.runYtDlp(ctx, t)
	}
	return fmt.Errorf("unsupported kind %q", kind)
}

func urlExt(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return naming.Ext(u.Path)
}

func (m *Manager) detectKind(ctx context.Context, job Job, headers map[string]string) (string, error) {
	low := strings.ToLower(job.URL)
	if strings.HasPrefix(low, "magnet:") {
		return KindTorrent, nil
	}
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		if _, err := os.Stat(job.URL); err == nil && strings.HasSuffix(low, ".torrent") {
			return KindTorrent, nil
		}
		return "", fmt.Errorf("unsupported URL scheme in %q", job.URL)
	}
	switch ext := urlExt(job.URL); {
	case ext == "torrent":
		return KindTorrent, nil
	case ext == "m3u8":
		return KindHLS, nil
	case ext == "mpd":
		return KindDASH, nil
	case naming.MediaExts[ext] || naming.FileExts[ext]:
		return KindFile, nil
	}
	c := m.client(headers)
	data, resp, err := c.GetBytes(ctx, job.URL, map[string]string{"Range": "bytes=0-65535"}, 65536)
	if err != nil {
		return "", err
	}
	ct := fetch.MediaType(resp)
	head := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 4096)])))
	switch {
	case strings.Contains(ct, "mpegurl") || strings.HasPrefix(head, "#extm3u"):
		return KindHLS, nil
	case strings.Contains(ct, "dash+xml") || strings.Contains(head, "<mpd"):
		return KindDASH, nil
	case ct == "application/x-bittorrent":
		return KindTorrent, nil
	case strings.Contains(ct, "text/html") || strings.HasPrefix(head, "<!doctype html") || strings.Contains(head, "<html"):
		if ytdlp.Find(m.Config().YtDlpPath) == "" {
			return "", errors.New("URL is a web page and yt-dlp is not installed; open it in the browser with the VidHawk extension instead")
		}
		return KindYtDlp, nil
	}
	return KindFile, nil
}

func mediaTitle(job Job) string {
	t := job.Title
	if e := naming.Ext(t); e != "" && naming.MediaExts[e] {
		t = strings.TrimSuffix(t, t[len(t)-len(e)-1:])
	}
	if strings.TrimSpace(t) == "" {
		t = naming.TitleFromURL(job.URL)
	}
	return t
}

func (m *Manager) runFile(ctx context.Context, t *task) error {
	job := t.snap()
	cfg := m.Config()
	c := m.client(t.headers)
	info, err := httpdl.Probe(ctx, c, job.URL)
	if err != nil {
		return err
	}
	if ct := strings.ToLower(info.ContentType); strings.HasPrefix(ct, "text/html") && !isHTMLDownload(job.URL, info.Filename) {
		return errors.New("the server answered with a web page instead of the file — the link has probably expired or is bound to the browser session; reload the page and catch it again")
	}
	if job.ExpectedSize > 0 && info.Size > 0 && info.Size < job.ExpectedSize/2 {
		return fmt.Errorf("the server offered only %d bytes but the page announced %d — the link is expired or bound to the browser session; play the video again and catch it afresh", info.Size, job.ExpectedSize)
	}
	dest := job.Path
	if dest == "" || filepath.Dir(dest) != filepath.Clean(job.OutDir) {
		name := naming.FileName(job.Title, info.Filename, info.FinalURL, info.ContentType)
		dest = naming.UniquePath(job.OutDir, name)
	}
	t.mu.Lock()
	t.job.Title = filepath.Base(dest)
	t.job.Path = dest
	t.mu.Unlock()
	m.emit(t, true)
	if err := httpdl.Download(ctx, c, info, dest, httpdl.Options{Connections: cfg.Connections}, func(done, total int64) {
		m.report(t, done, total, done, "bytes", nil)
	}); err != nil {
		return err
	}
	if fi, err := os.Stat(dest); err == nil {
		if info.Size > 0 && fi.Size() < info.Size {
			return fmt.Errorf("incomplete download: got %d of %d bytes — the server cut the transfer short; use Resume to retry", fi.Size(), info.Size)
		}
		if job.ExpectedSize > 0 && fi.Size() < job.ExpectedSize/2 {
			return fmt.Errorf("the server sent only %d bytes but the page announced %d — the link is expired or bound to the browser session; play the video again and catch it afresh", fi.Size(), job.ExpectedSize)
		}
	}
	return nil
}

func isHTMLDownload(rawURL, cdName string) bool {
	e := naming.Ext(cdName)
	if e == "" {
		e = urlExt(rawURL)
	}
	return e == "html" || e == "htm"
}

func (m *Manager) runHLS(ctx context.Context, t *task) error {
	job := t.snap()
	cfg := m.Config()
	res, err := hls.Download(ctx, m.client(t.headers), job.URL, job.OutDir, mediaTitle(job),
		hls.Options{Quality: job.Quality, Concurrency: cfg.SegmentConcurrency, FFmpeg: ffmpeg.Find(cfg.FFmpegPath)},
		func(done, total int, bytes int64) { m.report(t, int64(done), int64(total), bytes, "segments", nil) })
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.job.Path = res.Path
	t.job.Title = filepath.Base(res.Path)
	t.mu.Unlock()
	return nil
}

func (m *Manager) runDASH(ctx context.Context, t *task) error {
	job := t.snap()
	cfg := m.Config()
	res, err := dash.Download(ctx, m.client(t.headers), job.URL, job.OutDir, mediaTitle(job),
		dash.Options{Quality: job.Quality, Concurrency: cfg.SegmentConcurrency, FFmpeg: ffmpeg.Find(cfg.FFmpegPath)},
		func(done, total int, bytes int64) { m.report(t, int64(done), int64(total), bytes, "segments", nil) })
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.job.Path = res.Path
	t.job.Title = filepath.Base(res.Path)
	t.mu.Unlock()
	return nil
}

func (m *Manager) runTorrent(ctx context.Context, t *task) error {
	job := t.snap()
	cfg := m.Config()
	c := m.client(t.headers)
	fetchBytes := func(ctx context.Context, u string) ([]byte, error) {
		b, _, err := c.GetBytes(ctx, u, nil, 32<<20)
		return b, err
	}
	path, err := torrent.Download(ctx, job.URL, fetchBytes,
		torrent.Options{DataDir: job.OutDir, ListenPort: cfg.Torrent.ListenPort, Seed: cfg.Torrent.SeedAfterComplete},
		func(done, total int64, rate float64, peers int, name string) {
			m.report(t, done, total, done, "bytes", func(j *Job) {
				j.Peers = peers
				if rate > 0 {
					j.Speed = rate
				}
				if name != "" && (j.Title == "" || j.Title == j.URL) {
					j.Title = name
				}
				if total == 0 {
					j.Detail = "fetching metadata"
				} else {
					j.Detail = ""
				}
			})
		})
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.job.Path = path
	t.mu.Unlock()
	return nil
}

func (m *Manager) runYtDlp(ctx context.Context, t *task) error {
	job := t.snap()
	cfg := m.Config()
	exe := ytdlp.Find(cfg.YtDlpPath)
	if exe == "" {
		return errors.New("yt-dlp is not installed (pip install yt-dlp)")
	}
	target := job.PageURL
	if target == "" || job.Kind == KindYtDlp {
		target = job.URL
	}
	path, err := ytdlp.Download(ctx, exe, target, job.OutDir, t.headers, job.Quality, func(done, total int64, rate float64) {
		m.report(t, done, total, done, "bytes", func(j *Job) {
			if rate > 0 {
				j.Speed = rate
			}
		})
	})
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.job.Path = path
	t.job.Title = filepath.Base(path)
	t.mu.Unlock()
	return nil
}
