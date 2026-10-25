// Package ytdlp is the fallback resolver for pages the sniffers cannot handle
// (heavy JavaScript players, signed URLs): it shells out to yt-dlp.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/amminvention/vidhawk/internal/proc"
)

// Find returns the yt-dlp executable or "".
func Find(configured string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
	}
	if p, err := exec.LookPath("yt-dlp"); err == nil {
		return p
	}
	return ""
}

type Progress func(done, total int64, rate float64)

func heightOf(quality string) int {
	q := strings.ToLower(strings.TrimSpace(quality))
	if q == "" || q == "best" || q == "worst" {
		return 0
	}
	h, _ := strconv.Atoi(strings.TrimSuffix(q, "p"))
	return h
}

// Download runs yt-dlp for url and returns the final file path.
func Download(ctx context.Context, exe, url, outDir string, headers map[string]string, quality string, progress Progress) (string, error) {
	args := []string{
		"--newline", "--no-colors", "--no-playlist", "--no-simulate",
		"--print", "after_move:filepath",
		"--progress-template", "download:VHP|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.total_bytes_estimate)s|%(progress.speed)s",
		"-o", filepath.Join(outDir, "%(title).150B.%(ext)s"),
		"--merge-output-format", "mp4",
	}
	if h := heightOf(quality); h > 0 {
		args = append(args, "-f", fmt.Sprintf("bv*[height<=%d]+ba/b[height<=%d]/b", h, h))
	}
	for key, name := range map[string]string{"referer": "Referer", "cookie": "Cookie", "user-agent": "User-Agent", "origin": "Origin"} {
		if v := headers[key]; v != "" {
			args = append(args, "--add-header", name+":"+v)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, exe, args...)
	proc.HideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start yt-dlp: %w", err)
	}
	final := ""
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "VHP|"):
			if progress != nil {
				parts := strings.Split(line, "|")
				if len(parts) >= 5 {
					done := num(parts[1])
					total := num(parts[2])
					if total == 0 {
						total = num(parts[3])
					}
					progress(done, total, float64(num(parts[4])))
				}
			}
		case line == "", strings.HasPrefix(line, "["), strings.HasPrefix(line, "WARNING"), strings.HasPrefix(line, "ERROR"):
		default:
			final = line
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 1500 {
			msg = msg[len(msg)-1500:]
		}
		return "", fmt.Errorf("yt-dlp failed: %v: %s", err, msg)
	}
	if final == "" {
		return "", fmt.Errorf("yt-dlp finished without reporting an output file")
	}
	return final, nil
}

func num(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "None" {
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}
