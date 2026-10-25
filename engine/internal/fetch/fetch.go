// Package fetch is the HTTP client used by every downloader: it forwards the
// browser's request headers (Referer, Cookie, ...) and retries transient
// failures with exponential backoff.
package fetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var forwarded = map[string]bool{
	"referer": true, "origin": true, "cookie": true, "user-agent": true, "authorization": true,
	"accept": true, "accept-language": true, "x-requested-with": true,
	"sec-fetch-dest": true, "sec-fetch-mode": true, "sec-fetch-site": true,
}

const (
	Retries      = 5
	maxBackoff   = 15 * time.Second
	readAttempts = 3
)

type Client struct {
	HTTP      *http.Client
	Headers   map[string]string // lower-case keys, already filtered
	UserAgent string
}

func New(headers map[string]string, userAgent string, verifyTLS bool) *Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: !verifyTLS}, //nolint:gosec // user-controlled opt-out
		ForceAttemptHTTP2:     true,
	}
	return &Client{HTTP: &http.Client{Transport: tr}, Headers: Normalize(headers), UserAgent: userAgent}
}

// Normalize lower-cases keys and drops headers we never forward.
func Normalize(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		k = strings.ToLower(strings.TrimSpace(k))
		if forwarded[k] && v != "" {
			out[k] = v
		}
	}
	return out
}

func (c *Client) NewRequest(ctx context.Context, method, rawURL string, extra map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	ua := c.UserAgent
	if v := c.Headers["user-agent"]; v != "" {
		ua = v
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	for k, v := range c.Headers {
		if k != "user-agent" {
			req.Header.Set(k, v)
		}
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	return req, nil
}

type StatusError struct {
	Status int
	URL    string
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d for %s", e.Status, e.URL) }

func Retryable(status int) bool {
	switch status {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// Get issues a GET with retries. On success the body is open; caller closes.
func (c *Client) Get(ctx context.Context, rawURL string, extra map[string]string) (*http.Response, error) {
	var lastErr error
	delay := time.Second
	for attempt := 0; attempt < Retries; attempt++ {
		req, err := c.NewRequest(ctx, http.MethodGet, rawURL, extra)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTP.Do(req)
		if err == nil {
			if resp.StatusCode < 400 {
				return resp, nil
			}
			resp.Body.Close()
			lastErr = &StatusError{resp.StatusCode, rawURL}
			if !Retryable(resp.StatusCode) {
				return nil, lastErr
			}
		} else {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxBackoff {
			delay *= 2
		}
	}
	return nil, fmt.Errorf("fetch %s: %w", rawURL, lastErr)
}

// GetBytes downloads a body (up to max bytes when max > 0), retrying read
// failures as well as connection failures.
func (c *Client) GetBytes(ctx context.Context, rawURL string, extra map[string]string, max int64) ([]byte, *http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < readAttempts; attempt++ {
		resp, err := c.Get(ctx, rawURL, extra)
		if err != nil {
			return nil, nil, err
		}
		var r io.Reader = resp.Body
		if max > 0 {
			r = io.LimitReader(resp.Body, max)
		}
		data, rerr := io.ReadAll(r)
		resp.Body.Close()
		if rerr == nil {
			return data, resp, nil
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		lastErr = rerr
		time.Sleep(time.Second * time.Duration(attempt+1))
	}
	return nil, nil, fmt.Errorf("read %s: %w", rawURL, lastErr)
}

// GetText is GetBytes decoded as a string (manifests are small text files).
func (c *Client) GetText(ctx context.Context, rawURL string, extra map[string]string, max int64) (string, *http.Response, error) {
	b, resp, err := c.GetBytes(ctx, rawURL, extra, max)
	if err != nil {
		return "", nil, err
	}
	return string(b), resp, nil
}

// MediaType returns the lower-cased Content-Type without parameters.
func MediaType(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
}
