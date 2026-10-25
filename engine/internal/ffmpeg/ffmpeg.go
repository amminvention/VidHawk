// Package ffmpeg locates and drives an ffmpeg binary for lossless remuxing.
package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/amminvention/vidhawk/internal/proc"
)

// Find returns a usable ffmpeg path: the configured one, PATH, or well-known
// Windows install locations. Empty when none is available.
func Find(configured string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		patterns := []string{
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Packages", "Gyan.FFmpeg*", "*", "bin", "ffmpeg.exe"),
			`C:\ffmpeg\bin\ffmpeg.exe`,
			filepath.Join(os.Getenv("ProgramFiles"), "ffmpeg", "bin", "ffmpeg.exe"),
		}
		for _, pat := range patterns {
			if m, _ := filepath.Glob(pat); len(m) > 0 {
				return m[0]
			}
		}
	}
	return ""
}

// Remux stream-copies the inputs into out (MP4 with faststart). With two
// inputs (video track + audio track) all streams of both are kept.
func Remux(ctx context.Context, exe, out string, inputs ...string) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	for _, in := range inputs {
		args = append(args, "-i", in)
	}
	if len(inputs) > 1 {
		for i := range inputs {
			args = append(args, "-map", fmt.Sprint(i))
		}
	}
	args = append(args, "-c", "copy", "-movflags", "+faststart", out)
	cmd := exec.CommandContext(ctx, exe, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	proc.HideWindow(cmd)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return fmt.Errorf("ffmpeg failed: %v: %s", err, msg)
	}
	return nil
}
