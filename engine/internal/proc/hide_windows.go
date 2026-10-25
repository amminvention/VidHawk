//go:build windows

package proc

import (
	"os/exec"
	"syscall"
)

// HideWindow stops console children (ffmpeg, yt-dlp) from flashing a window.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
