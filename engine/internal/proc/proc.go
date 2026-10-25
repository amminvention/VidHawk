// Package proc has small OS helpers for child processes and the shell.
package proc

import (
	"os"
	"path/filepath"
)

// Open reveals a downloaded file in the OS file manager (or opens a folder).
func Open(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		// File is gone (moved/deleted): open its folder instead.
		return openPath(filepath.Dir(path), true)
	}
	return openPath(path, fi.IsDir())
}
