//go:build !windows

package proc

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

func openPath(path string, isDir bool) error {
	if runtime.GOOS == "darwin" {
		if isDir {
			return exec.Command("open", path).Start()
		}
		return exec.Command("open", "-R", path).Start()
	}
	if !isDir {
		path = filepath.Dir(path)
	}
	return exec.Command("xdg-open", path).Start()
}
