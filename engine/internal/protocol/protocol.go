// Package protocol implements Chrome native-messaging framing (a 4-byte
// native-endian length prefix followed by UTF-8 JSON) and the request shape
// shared by the extension, the CLI and the local API.
package protocol

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Chrome refuses host->browser messages larger than 1 MiB.
const (
	MaxOutbound = 1 << 20
	MaxInbound  = 64 << 20
)

// Request is a command from a client. Unused fields are simply omitted.
type Request struct {
	ID      int64             `json:"id,omitempty"`
	Cmd     string            `json:"cmd"`
	URL     string            `json:"url,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	Title   string            `json:"title,omitempty"`
	Quality string            `json:"quality,omitempty"`
	PageURL string            `json:"pageUrl,omitempty"`
	OutDir  string            `json:"outDir,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	JobID   string            `json:"jobId,omitempty"`
	Path    string            `json:"path,omitempty"`
	Set     map[string]any    `json:"set,omitempty"`
	Size    int64             `json:"expectedSize,omitempty"`
}

// Read decodes one framed message. io.EOF means the peer closed the pipe.
func Read(r io.Reader) (json.RawMessage, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	n := binary.NativeEndian.Uint32(hdr[:])
	if n > MaxInbound {
		return nil, fmt.Errorf("protocol: message of %d bytes exceeds limit", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Writer serialises framed messages; safe for concurrent use.
type Writer struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: bufio.NewWriterSize(w, 64<<10)} }

func (w *Writer) Send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > MaxOutbound {
		return fmt.Errorf("protocol: outbound message of %d bytes exceeds %d", len(data), MaxOutbound)
	}
	var hdr [4]byte
	binary.NativeEndian.PutUint32(hdr[:], uint32(len(data)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.w.Write(data); err != nil {
		return err
	}
	return w.w.Flush()
}
