// Package hls parses and downloads HTTP Live Streaming playlists.
package hls

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type Variant struct {
	URL        string  `json:"url"`
	Bandwidth  int     `json:"bandwidth"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	Codecs     string  `json:"codecs,omitempty"`
	AudioGroup string  `json:"audioGroup,omitempty"`
	FrameRate  float64 `json:"frameRate,omitempty"`
}

// Label is the short quality tag used in file names, e.g. "1080p".
func (v Variant) Label() string {
	if v.Height > 0 {
		return fmt.Sprintf("%dp", v.Height)
	}
	if v.Bandwidth > 0 {
		return fmt.Sprintf("%dkbps", v.Bandwidth/1000)
	}
	return ""
}

type Rendition struct {
	Type     string `json:"type"`
	Group    string `json:"group"`
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	Language string `json:"language,omitempty"`
	Default  bool   `json:"default"`
}

type Key struct {
	Method string
	URI    string
	IV     []byte // nil → derive from sequence number
}

type ByteRange struct {
	Length int64
	Offset int64
}

type Segment struct {
	URL      string
	Duration float64
	Seq      int64
	Key      *Key
	Range    *ByteRange
	MapURL   string
	MapRange *ByteRange
}

type MediaPlaylist struct {
	Segments       []Segment
	TargetDuration float64
	EndList        bool
	FMP4           bool
}

func (p *MediaPlaylist) Duration() float64 {
	var d float64
	for _, s := range p.Segments {
		d += s.Duration
	}
	return d
}

// DRM reports whether any segment uses a key method we cannot decrypt.
func (p *MediaPlaylist) DRM() bool {
	for _, s := range p.Segments {
		if s.Key != nil && s.Key.Method != "AES-128" {
			return true
		}
	}
	return false
}

func (p *MediaPlaylist) Encrypted() bool {
	for _, s := range p.Segments {
		if s.Key != nil {
			return true
		}
	}
	return false
}

func IsMaster(text string) bool { return strings.Contains(text, "#EXT-X-STREAM-INF") }

// ParseAttrs parses an attribute list such as BANDWIDTH=1,CODECS="a,b".
func ParseAttrs(s string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(s); {
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(s[i : i+eq])
		i += eq + 1
		var val string
		if i < len(s) && s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				val = s[i+1:]
				i = len(s)
			} else {
				val = s[i+1 : i+1+end]
				i += end + 2
			}
			if c := strings.IndexByte(s[i:], ','); c >= 0 {
				i += c + 1
			} else {
				i = len(s)
			}
		} else {
			c := strings.IndexByte(s[i:], ',')
			if c < 0 {
				val = strings.TrimSpace(s[i:])
				i = len(s)
			} else {
				val = strings.TrimSpace(s[i : i+c])
				i += c + 1
			}
		}
		out[key] = val
	}
	return out
}

func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	u, err := url.Parse(ref)
	if err != nil || base == nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

func parseByteRange(s string) *ByteRange {
	if s == "" {
		return nil
	}
	parts := strings.SplitN(s, "@", 2)
	n, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return nil
	}
	br := &ByteRange{Length: n, Offset: -1}
	if len(parts) == 2 {
		if off, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
			br.Offset = off
		}
	}
	return br
}

func parseIV(s string) []byte {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	iv := make([]byte, 16)
	if len(b) > 16 {
		b = b[len(b)-16:]
	}
	copy(iv[16-len(b):], b)
	return iv
}

// ParseMaster returns the variant streams and alternate renditions.
func ParseMaster(text string, base *url.URL) ([]Variant, []Rendition, error) {
	var variants []Variant
	var renditions []Rendition
	var pending map[string]string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pending = ParseAttrs(line[len("#EXT-X-STREAM-INF:"):])
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			a := ParseAttrs(line[len("#EXT-X-MEDIA:"):])
			r := Rendition{Type: a["TYPE"], Group: a["GROUP-ID"], Name: a["NAME"], Language: a["LANGUAGE"], Default: a["DEFAULT"] == "YES"}
			if a["URI"] != "" {
				r.URL = resolve(base, a["URI"])
			}
			renditions = append(renditions, r)
		case strings.HasPrefix(line, "#"):
		case pending != nil:
			v := Variant{URL: resolve(base, line), Codecs: pending["CODECS"], AudioGroup: pending["AUDIO"]}
			v.Bandwidth, _ = strconv.Atoi(pending["BANDWIDTH"])
			if v.Bandwidth == 0 {
				v.Bandwidth, _ = strconv.Atoi(pending["AVERAGE-BANDWIDTH"])
			}
			if res := strings.ToLower(pending["RESOLUTION"]); strings.Contains(res, "x") {
				wh := strings.SplitN(res, "x", 2)
				v.Width, _ = strconv.Atoi(wh[0])
				v.Height, _ = strconv.Atoi(wh[1])
			}
			v.FrameRate, _ = strconv.ParseFloat(pending["FRAME-RATE"], 64)
			variants = append(variants, v)
			pending = nil
		}
	}
	if len(variants) == 0 {
		return nil, renditions, fmt.Errorf("hls: master playlist has no variant streams")
	}
	return variants, renditions, nil
}

// ParseMedia parses a media playlist into segments with their keys/maps.
func ParseMedia(text string, base *url.URL) (*MediaPlaylist, error) {
	pl := &MediaPlaylist{}
	var (
		key       *Key
		mapURL    string
		mapRange  *ByteRange
		seq       int64
		duration  float64
		byteRange *ByteRange
		drm       bool
	)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF:"):
			d := strings.SplitN(line[len("#EXTINF:"):], ",", 2)[0]
			duration, _ = strconv.ParseFloat(strings.TrimSpace(d), 64)
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.ParseInt(strings.TrimSpace(line[len("#EXT-X-MEDIA-SEQUENCE:"):]), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			pl.TargetDuration, _ = strconv.ParseFloat(strings.TrimSpace(line[len("#EXT-X-TARGETDURATION:"):]), 64)
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			a := ParseAttrs(line[len("#EXT-X-KEY:"):])
			method := strings.ToUpper(a["METHOD"])
			switch method {
			case "", "NONE":
				key = nil
			case "AES-128":
				key = &Key{Method: method, URI: resolve(base, a["URI"])}
				if iv := a["IV"]; iv != "" {
					key.IV = parseIV(iv)
				}
			default:
				key = &Key{Method: method, URI: a["URI"]}
				drm = true
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			a := ParseAttrs(line[len("#EXT-X-MAP:"):])
			mapURL = resolve(base, a["URI"])
			mapRange = parseByteRange(a["BYTERANGE"])
			if mapRange != nil && mapRange.Offset < 0 {
				mapRange.Offset = 0
			}
			pl.FMP4 = true
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			byteRange = parseByteRange(strings.TrimSpace(line[len("#EXT-X-BYTERANGE:"):]))
		case line == "#EXT-X-ENDLIST":
			pl.EndList = true
		case strings.HasPrefix(line, "#"):
		default:
			u := resolve(base, line)
			if byteRange != nil && byteRange.Offset < 0 {
				byteRange.Offset = 0
				if n := len(pl.Segments); n > 0 && pl.Segments[n-1].URL == u && pl.Segments[n-1].Range != nil {
					prev := pl.Segments[n-1].Range
					byteRange.Offset = prev.Offset + prev.Length
				}
			}
			pl.Segments = append(pl.Segments, Segment{URL: u, Duration: duration, Seq: seq, Key: key, Range: byteRange, MapURL: mapURL, MapRange: mapRange})
			seq++
			duration = 0
			byteRange = nil
		}
	}
	_ = drm
	if !pl.FMP4 && len(pl.Segments) > 0 {
		p := strings.ToLower(strings.SplitN(pl.Segments[0].URL, "?", 2)[0])
		for _, ext := range []string{".mp4", ".m4s", ".m4v", ".m4a", ".cmfv", ".cmfa"} {
			if strings.HasSuffix(p, ext) {
				pl.FMP4 = true
				break
			}
		}
	}
	return pl, nil
}

// SelectVariant picks by quality: "best" (default), "worst", or a height such
// as "720p"/"720" (highest variant not exceeding it).
func SelectVariant(vs []Variant, quality string) Variant {
	sorted := append([]Variant(nil), vs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Height != sorted[j].Height {
			return sorted[i].Height < sorted[j].Height
		}
		return sorted[i].Bandwidth < sorted[j].Bandwidth
	})
	q := strings.ToLower(strings.TrimSpace(quality))
	switch q {
	case "", "best":
		return sorted[len(sorted)-1]
	case "worst":
		return sorted[0]
	}
	h, err := strconv.Atoi(strings.TrimSuffix(q, "p"))
	if err != nil {
		return sorted[len(sorted)-1]
	}
	pick := sorted[0]
	for _, v := range sorted {
		if v.Height <= h {
			pick = v
		}
	}
	return pick
}

// PickAudio chooses the default (or first) rendition of an audio group.
func PickAudio(rs []Rendition, group string) *Rendition {
	var first *Rendition
	for i := range rs {
		r := &rs[i]
		if r.Type != "AUDIO" || r.Group != group || r.URL == "" {
			continue
		}
		if r.Default {
			return r
		}
		if first == nil {
			first = r
		}
	}
	return first
}
