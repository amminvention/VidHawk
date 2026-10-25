package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Send(Request{ID: 7, Cmd: "download", URL: "https://x/y.m3u8", Headers: map[string]string{"referer": "https://x/"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Send(map[string]any{"event": "job"}); err != nil {
		t.Fatal(err)
	}
	raw, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil || req.ID != 7 || req.URL != "https://x/y.m3u8" || req.Headers["referer"] == "" {
		t.Fatalf("decoded %+v err %v", req, err)
	}
	if _, err := Read(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestOutboundLimit(t *testing.T) {
	w := NewWriter(io.Discard)
	big := make([]byte, MaxOutbound)
	for i := range big {
		big[i] = 'a'
	}
	if err := w.Send(map[string]string{"x": string(big)}); err == nil {
		t.Fatal("expected size error")
	}
}
