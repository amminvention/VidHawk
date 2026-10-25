// Package naming derives good file names from page metadata, URLs and HTTP
// headers. It mirrors extension/lib/naming.js so both sides agree.
package naming

import (
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	illegalRe = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	spaceRe   = regexp.MustCompile(`\s+`)
	genericRe = regexp.MustCompile(`(?i)^(watch|watch online|watch free|detail page|details?|home|homepage|video|videos|player|stream|streaming|online|free|hd|full hd|official( site| website)?|untitled|download|episode|(?:an? )?(?:video|live|online) ?streaming (?:portal|platform|site|service|app)|\d+|[\s\W]*)$`)
	compactRe = regexp.MustCompile(`[\s._-]+`)
	// Site slogans that ride along in <title>/og:title and never name the video.
	boilerplateRe = regexp.MustCompile(`(?i)\b(?:your |the )?(?:video |live |online )?streaming (?:guide|portal|platform|site|service|hub|app)\b|\bwatch (?:movies?|series|tv shows?|anime|online|free|now)\b|\bfree (?:movies?|streaming|download|online)\b|\bonline free\b|\bofficial (?:web)?site\b|\bhome ?page\b|\bstream (?:movies|now|online)\b|\b(?:movies|series) online\b`)
	episodeRe     = regexp.MustCompile(`(?i)\bs\d{1,2}\s*[ -]?\s*e\d{1,3}\b|\bseason\s*\d|\bepisode\s*\d|\bep\.?\s*\d`)
	yearRe        = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
	wordRe        = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)
	sepRe         = regexp.MustCompile(`\s+[-|–—:•·»«/]+\s+`)
	counterRe     = regexp.MustCompile(`^\(\d+\)\s*`)
	extRe         = regexp.MustCompile(`\.([A-Za-z0-9]{1,5})$`)
	cdStarRe      = regexp.MustCompile(`(?i)filename\*\s*=\s*(?:utf-8|utf8)''([^;]+)`)
	cdPlainRe     = regexp.MustCompile(`(?i)filename\s*=\s*(?:"([^"]*)"|'([^']*)'|([^;]+))`)
)

const maxNameRunes = 150

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// MediaExts and FileExts are extensions VidHawk treats as downloadable.
var (
	MediaExts = setOf("mp4", "m4v", "webm", "mkv", "mov", "avi", "flv", "wmv", "mpg", "mpeg", "3gp", "ogv", "ts",
		"mp3", "m4a", "aac", "ogg", "oga", "opus", "flac", "wav", "wma")
	FileExts = setOf("zip", "rar", "7z", "tar", "gz", "bz2", "xz", "iso", "img", "exe", "msi", "dmg", "pkg", "deb",
		"rpm", "apk", "pdf", "torrent")
)

var mimeExt = map[string]string{
	"video/mp4": "mp4", "video/webm": "webm", "video/x-matroska": "mkv", "video/quicktime": "mov",
	"video/x-msvideo": "avi", "video/x-flv": "flv", "video/mpeg": "mpg", "video/mp2t": "ts", "video/3gpp": "3gp",
	"video/ogg": "ogv", "audio/mpeg": "mp3", "audio/mp4": "m4a", "audio/aac": "aac", "audio/ogg": "ogg",
	"audio/wav": "wav", "audio/x-wav": "wav", "audio/webm": "webm", "audio/flac": "flac", "audio/opus": "opus",
	"application/zip": "zip", "application/x-zip-compressed": "zip", "application/pdf": "pdf",
	"application/x-rar-compressed": "rar", "application/vnd.rar": "rar", "application/x-7z-compressed": "7z",
	"application/x-msdownload": "exe", "application/x-msi": "msi", "application/vnd.android.package-archive": "apk",
	"application/x-bittorrent": "torrent",
}

// Meta is the metadata snapshot the extension collects from a page.
type Meta struct {
	LD    string `json:"ld"`
	OG    string `json:"og"`
	TW    string `json:"tw"`
	H1    string `json:"h1"`
	H2    string `json:"h2"`
	Title string `json:"title"`
}

func siteWords(pageURL string) []string {
	u, err := url.Parse(pageURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(host, ".")
	if len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	var out []string
	for _, p := range parts {
		if len(p) > 2 {
			out = append(out, p)
		}
	}
	return out
}

func isJunk(part string, words []string) bool {
	q := strings.ToLower(strings.TrimSpace(spaceRe.ReplaceAllString(part, " ")))
	if q == "" || genericRe.MatchString(q) || boilerplateRe.MatchString(q) {
		return true
	}
	compact := compactRe.ReplaceAllString(q, "")
	for _, w := range words {
		if q == w || q == w+".com" || q == w+" tv" || compact == w || compact == w+"tv" || compact == w+"com" || (strings.Contains(compact, w) && len(compact) <= len(w)+8) {
			return true
		}
	}
	return false
}

// CleanTitle strips "(3)" counters, site names and boilerplate segments such
// as "Detail Page" or "Watch Online" from a page title.
func CleanTitle(raw, pageURL string) string {
	s := counterRe.ReplaceAllString(raw, "")
	s = strings.NewReplacer("▶", " ", "►", " ", "⏵", " ").Replace(s)
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if s == "" {
		return ""
	}
	words := siteWords(pageURL)
	parts := sepRe.Split(s, -1)
	for len(parts) > 1 && isJunk(parts[len(parts)-1], words) {
		parts = parts[:len(parts)-1]
	}
	for len(parts) > 1 && isJunk(parts[0], words) {
		parts = parts[1:]
	}
	return strings.TrimSpace(strings.Join(parts, " - "))
}

// score rewards distinctive words and year/season/episode markers and
// penalises site slogans; the source rank only breaks ties, so a specific
// document title beats a generic og:title.
func score(c string, rank int) float64 {
	s := float64(len(wordRe.FindAllString(c, 9)))
	if s > 8 {
		s = 8
	}
	if episodeRe.MatchString(c) {
		s += 4
	}
	if yearRe.MatchString(c) {
		s++
	}
	if boilerplateRe.MatchString(c) {
		s -= 6
	}
	return s + float64(5-rank)*0.1
}

// PickTitle returns the best-scoring cleaned candidate among JSON-LD,
// Open Graph, Twitter, h1 and the document title.
func PickTitle(m Meta, pageURL string) string {
	best, bestScore := "", -1e9
	for rank, c := range []string{m.LD, m.OG, m.TW, m.H1, m.H2, m.Title} {
		t := CleanTitle(c, pageURL)
		if n := utf8.RuneCountInString(t); n < 2 || n > 180 || genericRe.MatchString(t) {
			continue
		}
		if s := score(t, rank); s > bestScore {
			best, bestScore = t, s
		}
	}
	return best
}

// Sanitize makes a string safe as a file name on every OS and caps its length.
func Sanitize(name string) string {
	s := illegalRe.ReplaceAllString(name, "")
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	s = strings.TrimRight(s, ". ")
	if utf8.RuneCountInString(s) > maxNameRunes {
		if loc := extRe.FindStringSubmatchIndex(s); loc != nil {
			ext := s[loc[2]:loc[3]]
			stem := []rune(s[:loc[0]])
			if keep := maxNameRunes - len(ext) - 1; keep > 0 && len(stem) > keep {
				stem = stem[:keep]
			}
			s = strings.TrimRight(string(stem), ". ") + "." + ext
		} else {
			s = string([]rune(s)[:maxNameRunes])
		}
	}
	if s == "" {
		return "download"
	}
	return s
}

// Ext returns the lower-case extension of a name without the dot, or "".
func Ext(name string) string {
	m := extRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// URLFileName returns the decoded last path segment of a URL.
func URLFileName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.TrimRight(u.Path, "/")
	last := p[strings.LastIndex(p, "/")+1:]
	if dec, err := url.PathUnescape(last); err == nil {
		last = dec
	}
	return last
}

// TitleFromURL is the last resort title: file name without extension, else host.
func TitleFromURL(raw string) string {
	if n := extRe.ReplaceAllString(URLFileName(raw), ""); n != "" {
		return n
	}
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "download"
}

// ContentDispositionFilename extracts filename or filename* from a header.
func ContentDispositionFilename(h string) string {
	if h == "" {
		return ""
	}
	if m := cdStarRe.FindStringSubmatch(h); m != nil {
		v := strings.TrimSpace(m[1])
		if dec, err := url.PathUnescape(v); err == nil {
			v = dec
		}
		return cleanCD(v)
	}
	if _, params, err := mime.ParseMediaType(h); err == nil {
		if v := params["filename"]; v != "" {
			return cleanCD(v)
		}
	}
	if m := cdPlainRe.FindStringSubmatch(h); m != nil {
		for _, v := range m[1:] {
			if v != "" {
				return cleanCD(strings.TrimSpace(v))
			}
		}
	}
	return ""
}

func cleanCD(v string) string {
	return strings.TrimSpace(strings.NewReplacer("\\", "_", "/", "_").Replace(v))
}

// ExtFromMIME maps a Content-Type to a file extension, or "".
func ExtFromMIME(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	return mimeExt[ct]
}

// FileName picks the name for a direct file download.
func FileName(title, cdName, rawURL, contentType string) string {
	if cdName != "" {
		return Sanitize(cdName)
	}
	if e := Ext(title); title != "" && (MediaExts[e] || FileExts[e]) {
		return Sanitize(title)
	}
	ext := Ext(URLFileName(rawURL))
	if !(MediaExts[ext] || FileExts[ext]) {
		if e := ExtFromMIME(contentType); e != "" {
			ext = e
		}
	}
	base := title
	if base == "" {
		base = TitleFromURL(rawURL)
	}
	if ext != "" {
		return Sanitize(base + "." + ext)
	}
	return Sanitize(base)
}

// MediaName builds "Title [1080p].mp4".
func MediaName(title, ext, label string) string {
	base := title
	if base == "" {
		base = "video"
	}
	if label != "" {
		base += " [" + label + "]"
	}
	return Sanitize(base + "." + ext)
}

// UniquePath returns dir/name, or dir/name (n) when taken — unless a resume
// state file exists next to it, in which case the same path is reused.
func UniquePath(dir, name string) string {
	cand := filepath.Join(dir, name)
	if _, err := os.Stat(cand); err != nil {
		return cand
	}
	if _, err := os.Stat(cand + ".vidhawk"); err == nil {
		return cand
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		c := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(c); err != nil {
			return c
		}
	}
	return cand
}
