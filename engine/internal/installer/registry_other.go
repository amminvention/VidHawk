//go:build !windows

package installer

import (
	"os"
	"path/filepath"
)

func register(b Browser, _ string, data []byte) error {
	dir := unixDir(b)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, HostName+".json"), data, 0o644)
}

func unregister(b Browser) error {
	err := os.Remove(filepath.Join(unixDir(b), HostName+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func registered(b Browser) bool {
	_, err := os.Stat(filepath.Join(unixDir(b), HostName+".json"))
	return err == nil
}
