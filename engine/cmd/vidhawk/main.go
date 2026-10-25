// Command vidhawk is the VidHawk download engine: CLI, browser native host
// and local API server in one binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/amminvention/vidhawk/internal/api"
	"github.com/amminvention/vidhawk/internal/config"
	"github.com/amminvention/vidhawk/internal/engine"
	"github.com/amminvention/vidhawk/internal/ffmpeg"
	"github.com/amminvention/vidhawk/internal/host"
	"github.com/amminvention/vidhawk/internal/installer"
	"github.com/amminvention/vidhawk/internal/resolver/ytdlp"
	"github.com/amminvention/vidhawk/internal/store"
)

const usage = `VidHawk — hunts down any video, from any player.

Usage:
  vidhawk download <url> [-o dir] [-n name] [-k kind] [-q quality] [--referer R] [--cookie C] [-c N]
  vidhawk probe <url> [--referer R] [--cookie C]
  vidhawk serve [--port N]         run the local API for the desktop app
  vidhawk host                     run as browser native-messaging host
  vidhawk install [--browsers a,b] [--extension-id ID]...
  vidhawk uninstall [--browsers a,b]
  vidhawk config [key=value ...]   show or change settings
  vidhawk doctor                   check ffmpeg / yt-dlp / browser integration
  vidhawk version
`

func main() {
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		// Browsers launch the host with the extension origin as first argument.
		os.Exit(run([]string{"host"}))
	}
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "download", "dl":
		err = cmdDownload(rest)
	case "probe":
		err = cmdProbe(rest)
	case "serve":
		err = cmdServe(rest)
	case "host":
		err = cmdHost()
	case "install":
		err = cmdInstall(rest)
	case "uninstall":
		err = cmdUninstall(rest)
	case "config":
		err = cmdConfig(rest)
	case "doctor":
		err = cmdDoctor()
	case "version", "--version", "-v":
		fmt.Println("vidhawk", engine.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newManager() (*engine.Manager, func(), error) {
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		fmt.Fprintln(os.Stderr, "warning: config:", cfgErr)
	}
	st, err := store.Open(filepath.Join(config.Dir(), "vidhawk.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	return engine.New(cfg, st), func() { st.Close() }, nil
}

func headerFlags(fs *flag.FlagSet) func() map[string]string {
	referer := fs.String("referer", "", "Referer header")
	cookie := fs.String("cookie", "", "Cookie header")
	ua := fs.String("user-agent", "", "User-Agent header")
	origin := fs.String("origin", "", "Origin header")
	return func() map[string]string {
		h := map[string]string{}
		for k, v := range map[string]string{"referer": *referer, "cookie": *cookie, "user-agent": *ua, "origin": *origin} {
			if v != "" {
				h[k] = v
			}
		}
		return h
	}
}

func cmdDownload(args []string) error {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	out := fs.String("o", "", "output directory")
	name := fs.String("n", "", "title / file name")
	kind := fs.String("k", "auto", "kind: auto|file|hls|dash|torrent|ytdlp")
	quality := fs.String("q", "best", "quality: best|worst|1080p|720 ...")
	conns := fs.Int("c", 0, "connections for direct downloads")
	headers := headerFlags(fs)
	url, err := parsePositional(fs, args, "url")
	if err != nil {
		return err
	}
	mgr, closeFn, err := newManager()
	if err != nil {
		return err
	}
	defer closeFn()
	if *conns > 0 {
		if _, err := mgr.UpdateConfig(map[string]any{"connections": *conns}); err != nil {
			return err
		}
	}
	doneCh := make(chan engine.Job, 1)
	unsub := mgr.Subscribe(func(ev engine.Event) {
		if ev.Job == nil {
			return
		}
		printProgress(*ev.Job)
		if !ev.Job.Active() {
			select {
			case doneCh <- *ev.Job:
			default:
			}
		}
	})
	defer unsub()
	job, err := mgr.Submit(engine.Spec{URL: url, Kind: *kind, Title: *name, Quality: *quality, OutDir: *out, Headers: headers()})
	if err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "\ncancelling…")
			_ = mgr.Cancel(job.ID)
		case j := <-doneCh:
			fmt.Fprintln(os.Stderr)
			switch j.Status {
			case engine.StatusDone:
				fmt.Println(j.Path)
				return nil
			case engine.StatusCancelled, engine.StatusPaused:
				return errors.New("cancelled")
			default:
				return errors.New(j.Error)
			}
		}
	}
}

func printProgress(j engine.Job) {
	var line string
	switch {
	case j.Status == engine.StatusQueued:
		line = "queued"
	case j.Total > 0:
		line = fmt.Sprintf("%5.1f%%  %s/%s  %s/s", j.Percent, humanUnit(j.Done, j.Unit), humanUnit(j.Total, j.Unit), human(int64(j.Speed)))
	default:
		line = fmt.Sprintf("%s  %s/s", human(j.Bytes), human(int64(j.Speed)))
	}
	if j.Peers > 0 {
		line += fmt.Sprintf("  %d peers", j.Peers)
	}
	if j.Detail != "" {
		line += "  " + j.Detail
	}
	fmt.Fprintf(os.Stderr, "\r%-9s %-70s", j.Status, line)
}

func humanUnit(n int64, unit string) string {
	if unit == "segments" {
		return fmt.Sprintf("%d seg", n)
	}
	return human(n)
}

func human(n int64) string {
	const k = 1024
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= k && i < len(units)-1 {
		f /= k
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func parsePositional(fs *flag.FlagSet, args []string, what string) (string, error) {
	// Allow flags before or after the positional argument.
	var pos []string
	var flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && isValueFlag(fs, a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	if err := fs.Parse(flags); err != nil {
		return "", err
	}
	if len(pos) != 1 {
		return "", fmt.Errorf("expected exactly one %s argument", what)
	}
	return pos[0], nil
}

func isValueFlag(fs *flag.FlagSet, name string) bool {
	name = strings.TrimLeft(name, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return false
	}
	return true
}

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	headers := headerFlags(fs)
	url, err := parsePositional(fs, args, "url")
	if err != nil {
		return err
	}
	mgr, closeFn, err := newManager()
	if err != nil {
		return err
	}
	defer closeFn()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := mgr.Probe(ctx, url, headers())
	if err != nil {
		return err
	}
	fmt.Printf("kind: %s\n", res.Kind)
	if res.FinalURL != "" && res.FinalURL != url {
		fmt.Printf("url:  %s\n", res.FinalURL)
	}
	switch res.Kind {
	case engine.KindHLS, engine.KindDASH:
		if res.DRM {
			fmt.Println("DRM:  protected (cannot download)")
		}
		if res.Master {
			fmt.Println("variants:")
			for _, v := range res.Variants {
				fmt.Printf("  %-7s %dx%d  %d kbps  %s\n", v.Label, v.Width, v.Height, v.Bandwidth/1000, v.Codecs)
			}
			for _, a := range res.Audio {
				fmt.Printf("  audio: %s %s\n", a.Name, a.Language)
			}
		} else {
			fmt.Printf("segments: %d  duration: %.0fs  encrypted: %v  live: %v\n", res.Segments, res.Duration, res.Encrypted, res.Live)
		}
	case engine.KindFile:
		fmt.Printf("size: %s  type: %s  ranges: %v", human(res.Size), res.ContentType, res.AcceptRanges)
		if res.Filename != "" {
			fmt.Printf("  filename: %s", res.Filename)
		}
		fmt.Println()
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 0, "port (0 = random)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mgr, closeFn, err := newManager()
	if err != nil {
		return err
	}
	defer closeFn()
	srv, err := api.Start(mgr, config.Dir(), *port)
	if err != nil {
		return err
	}
	defer srv.Close()
	fmt.Printf("VidHawk engine %s listening on http://127.0.0.1:%d (endpoint file: %s)\n", engine.Version, srv.Port, api.EndpointPath(config.Dir()))
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	return nil
}

func cmdHost() error {
	_ = os.MkdirAll(config.Dir(), 0o755)
	logOut := io.Writer(io.Discard)
	if f, err := os.OpenFile(filepath.Join(config.Dir(), "host.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		logOut = f
	}
	logger := log.New(logOut, "", log.LstdFlags)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var be engine.Backend
	if client, err := api.Connect(config.Dir()); err == nil {
		logger.Printf("proxying to running engine")
		be = client
	} else {
		mgr, closeFn, err := newManager()
		if err != nil {
			return err
		}
		defer closeFn()
		be = mgr
		logger.Printf("in-process engine %s", engine.Version)
	}
	return host.Run(ctx, be, os.Stdin, os.Stdout, logger)
}

func browsersFlag(fs *flag.FlagSet) func() []string {
	v := fs.String("browsers", "", "comma-separated: chrome,edge,brave,chromium,vivaldi (default all)")
	return func() []string {
		if *v == "" {
			return nil
		}
		return strings.Split(*v, ",")
	}
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	browsers := browsersFlag(fs)
	var ids multiFlag
	fs.Var(&ids, "extension-id", "additional extension ID to allow (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	done, err := installer.Install(exe, browsers(), ids)
	for _, b := range done {
		fmt.Printf("registered native host for %s\n", b)
	}
	if err != nil {
		return err
	}
	fmt.Printf("\nmanifest: %s\nengine:   %s\n\nNow load the extension: open chrome://extensions, enable Developer mode,\n\"Load unpacked\" → the extension/ folder. Extension ID: %s\n", installer.ManifestPath(), exe, installer.DefaultExtensionID)
	return nil
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	browsers := browsersFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	done, err := installer.Uninstall(browsers())
	for _, b := range done {
		fmt.Printf("removed native host for %s\n", b)
	}
	return err
}

func cmdConfig(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) > 0 {
		patch := map[string]any{}
		for _, kv := range args {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("expected key=value, got %q", kv)
			}
			patch[k] = coerce(v)
		}
		if cfg, err = cfg.Merge(patch); err != nil {
			return err
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
	}
	fmt.Printf("config file: %s\n", config.Path())
	fmt.Printf("downloadDir=%s\nconnections=%d\nsegmentConcurrency=%d\nmaxJobs=%d\nffmpegPath=%s\nytDlpPath=%s\nverifyTLS=%v\ntorrent.listenPort=%d\ntorrent.seedAfterComplete=%v\n",
		cfg.DownloadDir, cfg.Connections, cfg.SegmentConcurrency, cfg.MaxJobs, cfg.FFmpegPath, cfg.YtDlpPath, cfg.VerifyTLS, cfg.Torrent.ListenPort, cfg.Torrent.SeedAfterComplete)
	return nil
}

func coerce(v string) any {
	switch strings.ToLower(v) {
	case "true":
		return true
	case "false":
		return false
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil && fmt.Sprint(n) == v {
		return n
	}
	return v
}

func cmdDoctor() error {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("config:  INVALID -", err)
	} else {
		fmt.Println("config: ", config.Path())
	}
	fmt.Println("version:", engine.Version)
	if p := ffmpeg.Find(cfg.FFmpegPath); p != "" {
		fmt.Println("ffmpeg:  OK", p)
	} else {
		fmt.Println("ffmpeg:  MISSING (HLS/DASH will be saved without remuxing; install ffmpeg)")
	}
	if p := ytdlp.Find(cfg.YtDlpPath); p != "" {
		fmt.Println("yt-dlp:  OK", p)
	} else {
		fmt.Println("yt-dlp:  missing (optional; needed for YouTube-class sites)")
	}
	fmt.Println("torrent: OK (built in)")
	fmt.Println("download dir:", cfg.DownloadDir)
	for name, ok := range installer.Installed() {
		state := "not registered"
		if ok {
			state = "registered"
		}
		fmt.Printf("browser %-9s %s\n", name+":", state)
	}
	if ep, err := api.ReadEndpoint(config.Dir()); err == nil {
		fmt.Printf("engine api: port %d pid %d\n", ep.Port, ep.PID)
	} else {
		fmt.Println("engine api: not running (desktop app starts it)")
	}
	return nil
}
