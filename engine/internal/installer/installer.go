// Package installer registers the engine as a browser native-messaging host.
package installer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/amminvention/vidhawk/internal/config"
)

const (
	HostName           = "com.vidhawk.host"
	DefaultExtensionID = "nbpppdcnpheeihphpfihofafmhmnhhfd"
)

type Browser struct {
	Name     string
	WinKey   string // HKCU subkey
	MacDir   string // under ~/Library/Application Support
	LinuxDir string // under ~/.config
}

var Browsers = []Browser{
	{"chrome", `Software\Google\Chrome`, "Google/Chrome", "google-chrome"},
	{"edge", `Software\Microsoft\Edge`, "Microsoft Edge", "microsoft-edge"},
	{"brave", `Software\BraveSoftware\Brave-Browser`, "BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Browser"},
	{"chromium", `Software\Chromium`, "Chromium", "chromium"},
	{"vivaldi", `Software\Vivaldi`, "Vivaldi", "vivaldi"},
}

type Manifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func ManifestPath() string { return filepath.Join(config.Dir(), HostName+".json") }

func browserByName(name string) (Browser, bool) {
	for _, b := range Browsers {
		if b.Name == name {
			return b, true
		}
	}
	return Browser{}, false
}

func unixDir(b Browser) string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", b.MacDir, "NativeMessagingHosts")
	}
	return filepath.Join(home, ".config", b.LinuxDir, "NativeMessagingHosts")
}

// Install writes the host manifest pointing at exe and registers it for the
// named browsers (all known browsers when names is empty).
func Install(exe string, names []string, extraIDs []string) ([]string, error) {
	if exe == "" {
		return nil, errors.New("executable path is required")
	}
	ids := append([]string{DefaultExtensionID}, extraIDs...)
	origins := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			origins = append(origins, "chrome-extension://"+id+"/")
		}
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return nil, err
	}
	m := Manifest{Name: HostName, Description: "VidHawk download engine", Path: exe, Type: "stdio", AllowedOrigins: origins}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(ManifestPath(), data, 0o644); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		for _, b := range Browsers {
			names = append(names, b.Name)
		}
	}
	var done []string
	for _, name := range names {
		b, ok := browserByName(name)
		if !ok {
			return done, fmt.Errorf("unknown browser %q", name)
		}
		if err := register(b, ManifestPath(), data); err != nil {
			return done, fmt.Errorf("%s: %w", name, err)
		}
		done = append(done, name)
	}
	return done, nil
}

// Uninstall removes registrations and the manifest.
func Uninstall(names []string) ([]string, error) {
	if len(names) == 0 {
		for _, b := range Browsers {
			names = append(names, b.Name)
		}
	}
	var done []string
	for _, name := range names {
		b, ok := browserByName(name)
		if !ok {
			return done, fmt.Errorf("unknown browser %q", name)
		}
		if err := unregister(b); err != nil {
			return done, fmt.Errorf("%s: %w", name, err)
		}
		done = append(done, name)
	}
	_ = os.Remove(ManifestPath())
	return done, nil
}

// Installed reports which browsers currently have a registration.
func Installed() map[string]bool {
	out := map[string]bool{}
	for _, b := range Browsers {
		out[b.Name] = registered(b)
	}
	return out
}

// ExtensionIDFromKey derives the Chrome extension ID from a manifest "key".
func ExtensionIDFromKey(keyB64 string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	h := hex.EncodeToString(sum[:])[:32]
	out := make([]byte, 32)
	for i := 0; i < 32; i++ {
		v, _ := strconv(h[i])
		out[i] = 'a' + v
	}
	return string(out), nil
}

func strconv(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	}
	return 0, fmt.Errorf("bad hex %q", c)
}
