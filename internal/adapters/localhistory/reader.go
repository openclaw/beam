package localhistory

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

// Reader projects one local project inventory. Filenames are resolved by the adapter only.
type Reader struct {
	Name, SourceHome string
	Files            func(context.Context, string) ([]File, error)
	Load             func(context.Context, File, string) (History, error)
}

func (r Reader) Call(ctx context.Context, q adapter.Request) (any, error) {
	var id, b string
	var err error
	if q.Op == "read" {
		id, b, err = Selection(q.Selection)
		if err != nil {
			return nil, err
		}
	}
	files, err := r.Files(ctx, id)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	for i := 1; i < len(files); i++ {
		if files[i].ID == files[i-1].ID {
			return nil, errors.New("ambiguous session ID in selected store")
		}
	}
	if q.Op == "list" {
		entries := make([]adapter.Entry, 0, len(files))
		for _, f := range files {
			entries = append(entries, adapter.Entry{Selection: f.ID, Title: f.Title})
		}
		start, end, next, err := Page(r.Name+r.SourceHome, entries, len(entries), q.Cursor, q.Limit)
		if err != nil {
			return nil, pageError(err)
		}
		return adapter.ListResult{Entries: entries[start:end], NextCursor: next}, nil
	}
	for _, f := range files {
		if f.ID != id {
			continue
		}
		h, err := r.Load(ctx, f, b)
		if err != nil {
			return nil, pageError(err)
		}
		title := h.Title
		if title == "" {
			title = f.Title
		}
		if h.UpdatedAt == "" {
			h.UpdatedAt = f.Updated.UTC().Format(time.RFC3339Nano)
		}
		c := clean.Candidate{Origin: clean.Origin{Adapter: r.Name, SourceHome: r.SourceHome, NativeSession: id}, Source: r.Name, Branch: b, Title: packet.Clip(title, 160), UpdatedAt: h.UpdatedAt, Completeness: "complete"}
		if h.Partial {
			c.Completeness = "partial"
		}
		start, end, next, err := Page(r.Name+r.SourceHome+q.Selection, h, len(h.Items), q.Cursor, q.Limit)
		if err != nil {
			return nil, pageError(err)
		}
		if len(h.Items) == 0 {
			return nil, errors.New("selected session contains no shareable messages")
		}
		c.Items = make([]clean.Item, 0, end-start)
		for _, v := range h.Items[start:end] {
			c.Items = append(c.Items, clean.Item{Type: v.Type, Text: v.Text})
		}
		return adapter.ReadResult{Candidate: c, NextCursor: next}, nil
	}
	return nil, errors.New("session was not found in the selected project")
}

func pageError(err error) error {
	if err.Error() == "source_changed" {
		return adapter.Fail("source_changed", "after_refresh", "native history changed; repeat the exact selection")
	}
	return err
}

func Capabilities(ctx context.Context, name string) adapter.Capabilities {
	caps := adapter.Capabilities{Adapter: name, Protocol: 1, Version: "unavailable", Source: []string{"list", "read"}, Destination: []string{}, Recovery: "manual"}
	bin, err := exec.LookPath(name)
	if err != nil {
		return caps
	}
	// Probe from a neutral directory: version detection must not activate the current project's config.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 250 * time.Millisecond
	var output boundedVersion
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return caps
	}
	data := output.buf.Bytes()
	version := strings.TrimSpace(string(data))
	if version == "" || strings.ContainsAny(version, "\r\n\x1b") {
		return caps
	}
	caps.Version = version
	caps.Destination = []string{"stageReference", "resolve"}
	caps.Recovery = "lookupByOperationId"
	return caps
}

// Keep a mislabeled executable from streaming arbitrary output into the doctor response.
type boundedVersion struct{ buf bytes.Buffer }

func (b *boundedVersion) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > 1024 {
		return 0, errors.New("unexpected version output")
	}
	return b.buf.Write(p)
}
