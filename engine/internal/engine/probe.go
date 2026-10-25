package engine

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/amminvention/vidhawk/internal/dash"
	"github.com/amminvention/vidhawk/internal/fetch"
	"github.com/amminvention/vidhawk/internal/hls"
	"github.com/amminvention/vidhawk/internal/httpdl"
	"github.com/amminvention/vidhawk/internal/naming"
)

// Quality describes one selectable rendition of a stream.
type Quality struct {
	Label     string `json:"label"`
	Height    int    `json:"height,omitempty"`
	Width     int    `json:"width,omitempty"`
	Bandwidth int    `json:"bandwidth,omitempty"`
	Codecs    string `json:"codecs,omitempty"`
	URL       string `json:"url,omitempty"`
}

type AudioTrack struct {
	Name      string `json:"name,omitempty"`
	Language  string `json:"language,omitempty"`
	Bandwidth int    `json:"bandwidth,omitempty"`
}

// ProbeResult is what the popup / desktop app shows before downloading.
type ProbeResult struct {
	Kind         string       `json:"kind"`
	FinalURL     string       `json:"finalUrl,omitempty"`
	Master       bool         `json:"master,omitempty"`
	Variants     []Quality    `json:"variants,omitempty"`
	Audio        []AudioTrack `json:"audio,omitempty"`
	Segments     int          `json:"segments,omitempty"`
	Duration     float64      `json:"duration,omitempty"`
	Encrypted    bool         `json:"encrypted,omitempty"`
	DRM          bool         `json:"drm,omitempty"`
	Live         bool         `json:"live,omitempty"`
	Size         int64        `json:"size,omitempty"`
	ContentType  string       `json:"contentType,omitempty"`
	Filename     string       `json:"filename,omitempty"`
	AcceptRanges bool         `json:"acceptRanges,omitempty"`
}

func (m *Manager) Probe(ctx context.Context, rawURL string, headers map[string]string) (*ProbeResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	low := strings.ToLower(rawURL)
	if strings.HasPrefix(low, "magnet:") {
		return &ProbeResult{Kind: KindTorrent}, nil
	}
	c := m.client(headers)
	if ext := urlExt(rawURL); naming.MediaExts[ext] || naming.FileExts[ext] {
		return probeFile(ctx, c, rawURL)
	}
	data, resp, err := c.GetBytes(ctx, rawURL, map[string]string{"Range": "bytes=0-2097151"}, 2<<20)
	if err != nil {
		return nil, err
	}
	ct := fetch.MediaType(resp)
	text := string(data)
	head := strings.ToLower(strings.TrimSpace(text[:min(len(text), 4096)]))
	base := resp.Request.URL
	switch {
	case strings.HasPrefix(head, "#extm3u") || strings.Contains(ct, "mpegurl"):
		if hls.IsMaster(text) {
			variants, renditions, err := hls.ParseMaster(text, base)
			if err != nil {
				return nil, err
			}
			res := &ProbeResult{Kind: KindHLS, FinalURL: base.String(), Master: true}
			for _, v := range sortedVariants(variants) {
				res.Variants = append(res.Variants, Quality{Label: v.Label(), Height: v.Height, Width: v.Width, Bandwidth: v.Bandwidth, Codecs: v.Codecs, URL: v.URL})
			}
			for _, r := range renditions {
				if r.Type == "AUDIO" {
					res.Audio = append(res.Audio, AudioTrack{Name: r.Name, Language: r.Language})
				}
			}
			return res, nil
		}
		pl, err := hls.ParseMedia(text, base)
		if err != nil {
			return nil, err
		}
		return &ProbeResult{Kind: KindHLS, FinalURL: base.String(), Segments: len(pl.Segments), Duration: pl.Duration(),
			Encrypted: pl.Encrypted(), DRM: pl.DRM(), Live: !pl.EndList}, nil
	case strings.Contains(head, "<mpd") || strings.Contains(ct, "dash+xml"):
		reps, err := dash.ParseMPD(text, base)
		if err == dash.ErrDRM {
			return &ProbeResult{Kind: KindDASH, FinalURL: base.String(), DRM: true}, nil
		}
		if err != nil {
			return nil, err
		}
		res := &ProbeResult{Kind: KindDASH, FinalURL: base.String(), Master: true}
		for _, r := range dash.Videos(reps) {
			res.Variants = append(res.Variants, Quality{Label: r.Label(), Height: r.Height, Width: r.Width, Bandwidth: r.Bandwidth, Codecs: r.Codecs})
		}
		for _, r := range dash.Audios(reps) {
			res.Audio = append(res.Audio, AudioTrack{Name: r.ID, Language: r.Lang, Bandwidth: r.Bandwidth})
		}
		return res, nil
	case ct == "application/x-bittorrent" || urlExt(rawURL) == "torrent":
		return &ProbeResult{Kind: KindTorrent, FinalURL: base.String()}, nil
	case strings.Contains(ct, "text/html") || strings.HasPrefix(head, "<!doctype html") || strings.Contains(head, "<html"):
		return &ProbeResult{Kind: KindYtDlp, FinalURL: base.String(), ContentType: ct}, nil
	}
	res := &ProbeResult{Kind: KindFile, FinalURL: base.String(), ContentType: ct,
		Filename: naming.ContentDispositionFilename(resp.Header.Get("Content-Disposition"))}
	if resp.StatusCode == http.StatusPartialContent {
		res.AcceptRanges = true
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				res.Size, _ = strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
			}
		}
	} else {
		res.Size = resp.ContentLength
		res.AcceptRanges = strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes")
	}
	return res, nil
}

func probeFile(ctx context.Context, c *fetch.Client, rawURL string) (*ProbeResult, error) {
	info, err := httpdl.Probe(ctx, c, rawURL)
	if err != nil {
		return nil, err
	}
	return &ProbeResult{Kind: KindFile, FinalURL: info.FinalURL, Size: info.Size, ContentType: info.ContentType,
		Filename: info.Filename, AcceptRanges: info.Ranges}, nil
}

func sortedVariants(vs []hls.Variant) []hls.Variant {
	out := append([]hls.Variant(nil), vs...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && better(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func better(a, b hls.Variant) bool {
	if a.Height != b.Height {
		return a.Height > b.Height
	}
	return a.Bandwidth > b.Bandwidth
}
