package httpdl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/amminvention/vidhawk/internal/fetch"
)

func testServer(t *testing.T, data []byte, ranges bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Disposition", `attachment; filename="blob.bin"`)
		if ranges {
			w.Header().Set("Accept-Ranges", "bytes")
			http.ServeContent(w, r, "blob.bin", modTime, bytes.NewReader(data))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestParallelDownloadMatches(t *testing.T) {
	data := make([]byte, 5<<20+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	srv, hits := testServer(t, data, true)
	c := fetch.New(nil, "test", true)
	info, err := Probe(context.Background(), c, srv.URL+"/blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Ranges || info.Size != int64(len(data)) || info.Filename != "blob.bin" {
		t.Fatalf("probe: %+v", info)
	}
	dest := filepath.Join(t.TempDir(), "out.bin")
	var last int64
	err = Download(context.Background(), c, info, dest, Options{Connections: 4, MinChunk: 256 << 10}, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if sha256.Sum256(got) != sha256.Sum256(data) || last != int64(len(data)) {
		t.Fatal("content mismatch")
	}
	if _, err := os.Stat(dest + ".vidhawk"); !os.IsNotExist(err) {
		t.Fatal("state file should be removed after completion")
	}
	if hits.Load() < 5 {
		t.Fatalf("expected parallel range requests, got %d hits", hits.Load())
	}
}

func TestResumeFromState(t *testing.T) {
	data := make([]byte, 3<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	srv, _ := testServer(t, data, true)
	c := fetch.New(nil, "test", true)
	info, _ := Probe(context.Background(), c, srv.URL+"/blob.bin")
	dest := filepath.Join(t.TempDir(), "out.bin")

	ctx, cancel := context.WithCancel(context.Background())
	_ = Download(ctx, c, info, dest, Options{Connections: 2, MinChunk: 256 << 10}, func(done, total int64) {
		if done > 512<<10 {
			cancel()
		}
	})
	if _, err := os.Stat(dest + ".vidhawk"); err != nil {
		t.Fatal("state file expected after cancellation")
	}
	if err := Download(context.Background(), c, info, dest, Options{Connections: 2, MinChunk: 256 << 10}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("resumed content mismatch")
	}
}

func TestSingleStreamFallback(t *testing.T) {
	data := bytes.Repeat([]byte("vidhawk"), 100000)
	srv, _ := testServer(t, data, false)
	c := fetch.New(nil, "test", true)
	info, err := Probe(context.Background(), c, srv.URL+"/x")
	if err != nil {
		t.Fatal(err)
	}
	if info.Ranges {
		t.Fatal("server does not support ranges")
	}
	dest := filepath.Join(t.TempDir(), "single.bin")
	if err := Download(context.Background(), c, info, dest, Options{Connections: 8}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("single-stream content mismatch")
	}
}
