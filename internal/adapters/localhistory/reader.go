package localhistory

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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
		w, err := page(r.Name+r.SourceHome, entries, len(entries), q.Cursor, q.Limit)
		if err != nil {
			return nil, pageError(err)
		}
		return adapter.ListResult{Entries: entries[w.start:w.end], NextCursor: w.next()}, nil
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
		w, err := page(r.Name+r.SourceHome+q.Selection, h, len(h.Items), q.Cursor, q.Limit)
		if err != nil {
			return nil, pageError(err)
		}
		if len(h.Items) == 0 {
			return nil, errors.New("selected session contains no shareable messages")
		}
		// Reserve space for the protocol envelope and next cursor before adding full native messages.
		header, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		remaining := adapter.MaxResponseBytes - len(header) - 192
		for i := w.start; i < w.end; i++ {
			item := clean.Item{Type: h.Items[i].Type, Text: h.Items[i].Text}
			body, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			if len(body)+1 > remaining {
				if i == w.start {
					return nil, errors.New("one selected native message exceeds the adapter response budget")
				}
				w.end = i
				break
			}
			remaining -= len(body) + 1
			c.Items = append(c.Items, item)
		}
		return adapter.ReadResult{Candidate: c, NextCursor: w.next()}, nil
	}
	return nil, errors.New("session was not found in the selected project")
}

func pageError(err error) error {
	if err.Error() == "source_changed" {
		return adapter.Fail("source_changed", "after_reconcile", "native history changed; repeat the exact selection")
	}
	return err
}
