// Package proc has small OS helpers for child processes and the shell.
package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Open reveals a downloaded file in the OS file manager (or opens a folder).
func Open(path string) error {
	fi, err := os.Stat(path)
	isDir := err == nil && fi.IsDir()
	switch runtime.GOOS {
	case "windows":
		if err == nil && !isDir {
			return exec.Command("explorer.exe", "/select,"+path).Start()
		}
		if err != nil {
			path = filepath.Dir(path)
		}
		return exec.Command("explorer.exe", path).Start()
	case "darwin":
		if err == nil && !isDir {
			return exec.Command("open", "-R", path).Start()
		}
		if err != nil {
			path = filepath.Dir(path)
		}
		return exec.Command("open", path).Start()
	default:
		if !isDir {
			path = filepath.Dir(path)
		}
		return exec.Command("xdg-open", path).Start()
	}
}
