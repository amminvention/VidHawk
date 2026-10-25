// Package config holds VidHawk's user configuration (~/.vidhawk/config.json).
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Torrent struct {
	ListenPort        int  `json:"listenPort"`
	Sequential        bool `json:"sequential"`
	SeedAfterComplete bool `json:"seedAfterComplete"`
}

type Config struct {
	DownloadDir        string  `json:"downloadDir"`
	Connections        int     `json:"connections"`
	SegmentConcurrency int     `json:"segmentConcurrency"`
	MaxJobs            int     `json:"maxJobs"`
	FFmpegPath         string  `json:"ffmpegPath"`
	YtDlpPath          string  `json:"ytDlpPath"`
	UserAgent          string  `json:"userAgent"`
	VerifyTLS          bool    `json:"verifyTLS"`
	Torrent            Torrent `json:"torrent"`
}

const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

// Dir is the VidHawk home directory ($VIDHAWK_HOME or ~/.vidhawk).
func Dir() string {
	if d := os.Getenv("VIDHAWK_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".vidhawk")
}

func Path() string { return filepath.Join(Dir(), "config.json") }

func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return Config{
		DownloadDir:        filepath.Join(home, "Downloads", "VidHawk"),
		Connections:        8,
		SegmentConcurrency: 8,
		MaxJobs:            3,
		UserAgent:          DefaultUserAgent,
		VerifyTLS:          true,
		Torrent:            Torrent{ListenPort: 6881},
	}
}

// Load reads the config, filling missing fields with defaults. A missing file
// is not an error; a corrupt file returns defaults plus the parse error.
func Load() (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	return cfg.normalized(), nil
}

func (c Config) normalized() Config {
	if c.Connections < 1 {
		c.Connections = 1
	}
	if c.SegmentConcurrency < 1 {
		c.SegmentConcurrency = 1
	}
	if c.MaxJobs < 1 {
		c.MaxJobs = 1
	}
	if c.UserAgent == "" {
		c.UserAgent = DefaultUserAgent
	}
	if c.DownloadDir == "" {
		c.DownloadDir = Default().DownloadDir
	}
	return c
}

func Save(c Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c.normalized(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0o644)
}

// Merge applies a partial JSON object (e.g. {"connections": 16}) on top of c.
func (c Config) Merge(patch map[string]any) (Config, error) {
	cur, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	var m map[string]any
	if err := json.Unmarshal(cur, &m); err != nil {
		return c, err
	}
	for k, v := range patch {
		if sub, ok := v.(map[string]any); ok {
			if existing, ok := m[k].(map[string]any); ok {
				for sk, sv := range sub {
					existing[sk] = sv
				}
				continue
			}
		}
		m[k] = v
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return c, err
	}
	out := c
	if err := json.Unmarshal(merged, &out); err != nil {
		return c, err
	}
	return out.normalized(), nil
}
