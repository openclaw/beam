// Package file reads explicitly selected transcripts and writes reviewed artifacts for manual import.
package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

type Adapter struct{ OutputPath string }

func (a Adapter) Call(ctx context.Context, r adapter.Request) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch r.Op {
	case "capabilities":
		return adapter.Capabilities{Protocol: 1, Adapter: "file", Version: "1", Source: []string{"read"}, Destination: []string{"stageReference"}, Recovery: "manual"}, nil
	case "read":
		return read(r)
	case "stageReference":
		path, err := write(a.OutputPath, []byte(packet.Render(r.Packet)))
		if err != nil {
			return nil, err
		}
		return adapter.OpenResult{State: "manual", Artifact: path, NextAction: "Attach the reviewed Markdown file in the destination harness."}, nil
	default:
		return nil, adapter.Unsupported(r.Op)
	}
}

func ReadPacket(data []byte) (packet.Packet, error) {
	p, err := packet.Parse(data)
	if err == nil {
		return p, nil
	}
	v1, v1err := packet.ImportV1(data)
	if v1err == nil {
		return v1, nil
	}
	return packet.Packet{}, err
}

func read(r adapter.Request) (adapter.ReadResult, error) {
	path, err := filepath.Abs(r.Selection)
	if err != nil {
		return adapter.ReadResult{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return adapter.ReadResult{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return adapter.ReadResult{}, err
	}
	if !info.Mode().IsRegular() {
		return adapter.ReadResult{}, fmt.Errorf("choose a regular file")
	}
	b, err := adapter.ReadBounded(f, clean.MaxCandidateBytes)
	if err != nil {
		return adapter.ReadResult{}, err
	}
	if !utf8.Valid(b) {
		return adapter.ReadResult{}, fmt.Errorf("file is not valid UTF-8")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return adapter.ReadResult{}, err
	}
	c := clean.Candidate{Source: "file", Origin: clean.Origin{Adapter: "file", SourceHome: home, NativeSession: path},
		Title: filepath.Base(path), UpdatedAt: info.ModTime().UTC().Format(time.RFC3339Nano), Completed: true, Completeness: "complete"}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		p, err := ReadPacket(b)
		if err != nil {
			return adapter.ReadResult{}, err
		}
		c.Title, c.UpdatedAt, c.Completed, c.SourceModel = p.Title, p.UpdatedAt, p.Completed, p.SourceModel
		if p.Truncated {
			c.Completeness = "partial"
		}
		for _, i := range p.Items {
			c.Items = append(c.Items, clean.Item{Type: i.Type, Text: i.Text, SpeakerID: i.Speaker})
		}
	} else {
		c.Items = []clean.Item{{Type: "other", Text: string(b)}}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(b))
	start := 0
	if r.Cursor != "" {
		hash, n, ok := strings.Cut(r.Cursor, ":")
		if !ok || hash != digest {
			return adapter.ReadResult{}, adapter.Fail("source_changed", "never", "selected file changed; read it again")
		}
		start, err = strconv.Atoi(n)
		if err != nil || start < 0 || start >= len(c.Items) {
			return adapter.ReadResult{}, fmt.Errorf("invalid file cursor")
		}
	}
	if r.Limit < 1 || r.Limit > 200 {
		return adapter.ReadResult{}, fmt.Errorf("invalid file page size")
	}
	end := min(start+r.Limit, len(c.Items))
	result := adapter.ReadResult{Candidate: c}
	result.Items = c.Items[start:end]
	if end < len(c.Items) {
		result.NextCursor = fmt.Sprintf("%s:%d", digest, end)
	}
	return result, nil
}

func write(path string, data []byte) (string, error) {
	var f *os.File
	var err error
	if path == "" {
		f, err = os.CreateTemp(".", "beam-*.md")
	} else {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, bytes.NewReader(data))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return filepath.Abs(f.Name())
}
