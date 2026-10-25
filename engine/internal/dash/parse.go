// Package dash parses and downloads MPEG-DASH presentations (MPD).
package dash

import (
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrDRM is returned when the MPD carries ContentProtection descriptors.
var ErrDRM = errors.New("stream is DRM-protected (ContentProtection); cannot be downloaded")

// Rep is a downloadable representation: either a list of segment URLs with an
// optional init segment, or one single file (SegmentBase / plain BaseURL).
type Rep struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"` // video | audio
	Bandwidth int      `json:"bandwidth"`
	Width     int      `json:"width,omitempty"`
	Height    int      `json:"height,omitempty"`
	Codecs    string   `json:"codecs,omitempty"`
	Lang      string   `json:"lang,omitempty"`
	Mime      string   `json:"mime,omitempty"`
	Duration  float64  `json:"-"` // period duration in seconds (0 when unknown)
	InitURL   string   `json:"-"`
	Segments  []string `json:"-"`
	SingleURL string   `json:"-"`
}

func (r Rep) Label() string {
	if r.Height > 0 {
		return fmt.Sprintf("%dp", r.Height)
	}
	if r.Bandwidth > 0 {
		return fmt.Sprintf("%dkbps", r.Bandwidth/1000)
	}
	return ""
}

// Units is the number of download units (init + segments, or 1 single file).
func (r Rep) Units() int {
	if r.SingleURL != "" {
		return 1
	}
	n := len(r.Segments)
	if r.InitURL != "" {
		n++
	}
	return n
}

type mpd struct {
	MediaPresentationDuration string   `xml:"mediaPresentationDuration,attr"`
	BaseURL                   []string `xml:"BaseURL"`
	Periods                   []period `xml:"Period"`
}

type period struct {
	Duration string          `xml:"duration,attr"`
	BaseURL  []string        `xml:"BaseURL"`
	Sets     []adaptationSet `xml:"AdaptationSet"`
}

type adaptationSet struct {
	MimeType          string           `xml:"mimeType,attr"`
	ContentType       string           `xml:"contentType,attr"`
	Lang              string           `xml:"lang,attr"`
	BaseURL           []string         `xml:"BaseURL"`
	ContentProtection []struct{}       `xml:"ContentProtection"`
	SegmentTemplate   *segmentTemplate `xml:"SegmentTemplate"`
	SegmentList       *segmentList     `xml:"SegmentList"`
	SegmentBase       *segmentBase     `xml:"SegmentBase"`
	Reps              []representation `xml:"Representation"`
}

type representation struct {
	ID                string           `xml:"id,attr"`
	Bandwidth         int              `xml:"bandwidth,attr"`
	Width             int              `xml:"width,attr"`
	Height            int              `xml:"height,attr"`
	Codecs            string           `xml:"codecs,attr"`
	MimeType          string           `xml:"mimeType,attr"`
	BaseURL           []string         `xml:"BaseURL"`
	ContentProtection []struct{}       `xml:"ContentProtection"`
	SegmentTemplate   *segmentTemplate `xml:"SegmentTemplate"`
	SegmentList       *segmentList     `xml:"SegmentList"`
	SegmentBase       *segmentBase     `xml:"SegmentBase"`
}

type segmentTemplate struct {
	Initialization string `xml:"initialization,attr"`
	Media          string `xml:"media,attr"`
	StartNumber    *int64 `xml:"startNumber,attr"`
	Timescale      *int64 `xml:"timescale,attr"`
	Duration       *int64 `xml:"duration,attr"`
	Timeline       *struct {
		S []struct {
			T *int64 `xml:"t,attr"`
			D int64  `xml:"d,attr"`
			R *int64 `xml:"r,attr"`
		} `xml:"S"`
	} `xml:"SegmentTimeline"`
}

type segmentList struct {
	Initialization *struct {
		SourceURL string `xml:"sourceURL,attr"`
	} `xml:"Initialization"`
	URLs []struct {
		Media string `xml:"media,attr"`
	} `xml:"SegmentURL"`
}

type segmentBase struct{}

var (
	durRe = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:([\d.]+)S)?)?$`)
	tplRe = regexp.MustCompile(`\$(RepresentationID|Number|Time|Bandwidth)(%0\d+d)?\$`)
)

// ParseDuration parses an ISO 8601 duration like PT1H2M3.5S into seconds.
func ParseDuration(s string) float64 {
	m := durRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	d, _ := strconv.ParseFloat(zero(m[1]), 64)
	h, _ := strconv.ParseFloat(zero(m[2]), 64)
	mi, _ := strconv.ParseFloat(zero(m[3]), 64)
	sec, _ := strconv.ParseFloat(zero(m[4]), 64)
	return d*86400 + h*3600 + mi*60 + sec
}

func zero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func fill(tpl, repID string, number, t, bandwidth int64) string {
	out := tplRe.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := tplRe.FindStringSubmatch(m)
		var v any
		switch sub[1] {
		case "RepresentationID":
			return repID
		case "Number":
			v = number
		case "Time":
			v = t
		case "Bandwidth":
			v = bandwidth
		}
		if sub[2] != "" {
			return fmt.Sprintf(sub[2], v)
		}
		return fmt.Sprint(v)
	})
	return strings.ReplaceAll(out, "$$", "$")
}

func baseOf(parent *url.URL, bases []string) *url.URL {
	if len(bases) == 0 || strings.TrimSpace(bases[0]) == "" {
		return parent
	}
	u, err := url.Parse(strings.TrimSpace(bases[0]))
	if err != nil {
		return parent
	}
	if parent == nil {
		return u
	}
	return parent.ResolveReference(u)
}

func join(base *url.URL, ref string) string {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || base == nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

// ParseMPD extracts video/audio representations from the first Period.
func ParseMPD(text string, base *url.URL) ([]Rep, error) {
	var doc mpd
	if err := xml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("dash: invalid MPD: %w", err)
	}
	if len(doc.Periods) == 0 {
		return nil, errors.New("dash: MPD has no Period")
	}
	total := ParseDuration(doc.MediaPresentationDuration)
	mbase := baseOf(base, doc.BaseURL)
	p := doc.Periods[0]
	pdur := ParseDuration(p.Duration)
	if pdur == 0 {
		pdur = total
	}
	pbase := baseOf(mbase, p.BaseURL)

	var reps []Rep
	for _, set := range p.Sets {
		if len(set.ContentProtection) > 0 {
			return nil, ErrDRM
		}
		abase := baseOf(pbase, set.BaseURL)
		for _, r := range set.Reps {
			if len(r.ContentProtection) > 0 {
				return nil, ErrDRM
			}
			mimeType := firstNonEmpty(r.MimeType, set.MimeType)
			kind := strings.ToLower(firstNonEmpty(set.ContentType, strings.SplitN(mimeType, "/", 2)[0]))
			if kind != "video" && kind != "audio" {
				continue
			}
			rbase := baseOf(abase, r.BaseURL)
			rep := Rep{ID: r.ID, Kind: kind, Bandwidth: r.Bandwidth, Width: r.Width, Height: r.Height, Codecs: r.Codecs, Lang: set.Lang, Mime: mimeType, Duration: pdur}
			tpl := r.SegmentTemplate
			if tpl == nil {
				tpl = set.SegmentTemplate
			}
			list := r.SegmentList
			if list == nil {
				list = set.SegmentList
			}
			switch {
			case tpl != nil:
				if err := applyTemplate(&rep, tpl, rbase, pdur); err != nil {
					return nil, err
				}
			case list != nil:
				if list.Initialization != nil && list.Initialization.SourceURL != "" {
					rep.InitURL = join(rbase, list.Initialization.SourceURL)
				}
				for _, s := range list.URLs {
					rep.Segments = append(rep.Segments, join(rbase, s.Media))
				}
			default:
				rep.SingleURL = rbase.String()
			}
			if rep.SingleURL == "" && len(rep.Segments) == 0 {
				continue
			}
			reps = append(reps, rep)
		}
	}
	if len(reps) == 0 {
		return nil, errors.New("dash: no downloadable video/audio representations found")
	}
	return reps, nil
}

func applyTemplate(rep *Rep, tpl *segmentTemplate, rbase *url.URL, pdur float64) error {
	start := int64(1)
	if tpl.StartNumber != nil {
		start = *tpl.StartNumber
	}
	timescale := int64(1)
	if tpl.Timescale != nil && *tpl.Timescale > 0 {
		timescale = *tpl.Timescale
	}
	bw := int64(rep.Bandwidth)
	if tpl.Initialization != "" {
		rep.InitURL = join(rbase, fill(tpl.Initialization, rep.ID, 0, 0, bw))
	}
	if tpl.Media == "" {
		return fmt.Errorf("dash: representation %s has a SegmentTemplate without media", rep.ID)
	}
	if tpl.Timeline != nil {
		var t int64
		n := start
		periodTicks := int64(pdur * float64(timescale))
		for _, s := range tpl.Timeline.S {
			if s.T != nil {
				t = *s.T
			}
			if s.D <= 0 {
				continue
			}
			repeat := int64(0)
			if s.R != nil {
				repeat = *s.R
			}
			if repeat < 0 {
				if periodTicks > t {
					repeat = (periodTicks-t)/s.D - 1
					if (periodTicks-t)%s.D != 0 {
						repeat++
					}
				} else {
					repeat = 0
				}
			}
			for i := int64(0); i <= repeat; i++ {
				rep.Segments = append(rep.Segments, join(rbase, fill(tpl.Media, rep.ID, n, t, bw)))
				t += s.D
				n++
			}
		}
		return nil
	}
	if tpl.Duration == nil || *tpl.Duration <= 0 || pdur <= 0 {
		return fmt.Errorf("dash: representation %s: cannot determine segment count (live or unsupported MPD)", rep.ID)
	}
	count := int64(math.Ceil(pdur * float64(timescale) / float64(*tpl.Duration)))
	for i := int64(0); i < count; i++ {
		rep.Segments = append(rep.Segments, join(rbase, fill(tpl.Media, rep.ID, start+i, i**tpl.Duration, bw)))
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Videos / Audios filter by kind, highest quality first.
func Videos(reps []Rep) []Rep { return filterSorted(reps, "video") }
func Audios(reps []Rep) []Rep { return filterSorted(reps, "audio") }

func filterSorted(reps []Rep, kind string) []Rep {
	var out []Rep
	for _, r := range reps {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height > out[j].Height
		}
		return out[i].Bandwidth > out[j].Bandwidth
	})
	return out
}

// SelectVideo mirrors hls.SelectVariant: best, worst or a height cap.
func SelectVideo(videos []Rep, quality string) Rep {
	q := strings.ToLower(strings.TrimSpace(quality))
	switch q {
	case "", "best":
		return videos[0]
	case "worst":
		return videos[len(videos)-1]
	}
	h, err := strconv.Atoi(strings.TrimSuffix(q, "p"))
	if err != nil {
		return videos[0]
	}
	for _, v := range videos {
		if v.Height <= h {
			return v
		}
	}
	return videos[len(videos)-1]
}
