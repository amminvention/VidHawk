//go:build windows

package installer

import (
	"golang.org/x/sys/windows/registry"
)

func hostKey(b Browser) string { return b.WinKey + `\NativeMessagingHosts\` + HostName }

func register(b Browser, manifestPath string, _ []byte) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, hostKey(b), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("", manifestPath)
}

func unregister(b Browser) error {
	err := registry.DeleteKey(registry.CURRENT_USER, hostKey(b))
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func registered(b Browser) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, hostKey(b), registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}
