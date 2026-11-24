package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/nativepage"
	"github.com/openclaw/beam/internal/clean"
)

// The human history renderer flattens Text, Thinking and tool placeholders.
// The structured record preserves their types. Select only its visible blocks.
type messageContent struct{ Text string }
type turn struct{ Content []messageContent }
type nativeRecord struct {
	ID         string `json:"acpxRecordId"`
	NativeID   string `json:"acpSessionId"`
	Name       string
	Cwd        string
	Title      string
	LastUsedAt string
	UpdatedAt  string `json:"updated_at"`
	Closed     bool
	Messages   []json.RawMessage
}

type listed struct {
	adapter.Entry
	name string
}

func (a Adapter) metadata(ctx context.Context) ([]listed, error) {
	// The text list contains only id, name, cwd and time. JSON on acpx 0.22
	// includes full transcripts of all sessions, even for a discovery request.
	raw, err := a.run(ctx, "", adapter.MaxResponseBytes, "--format", "text", a.Agent, "sessions", "list", "--local", "--filter-cwd", ".")
	if err != nil {
		return nil, err
	}
	dir, err := a.cwd()
	if err != nil {
		return nil, err
	}
	entries := []listed{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line == "No sessions" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || filepath.Clean(fields[2]) != filepath.Clean(dir) {
			return nil, adapter.Fail("unavailable", "never", "acpx returned an ambiguous session listing")
		}
		id := strings.TrimSuffix(fields[0], " [closed]")
		if id == "" {
			return nil, adapter.Fail("unavailable", "never", "acpx returned an empty record ID")
		}
		name := fields[1]
		title := name
		if name == "-" {
			name = ""
			title = "Default session"
		}
		entries = append(entries, listed{adapter.Entry{Selection: id, Title: title}, name})
	}
	return entries, nil
}

func (a Adapter) list(ctx context.Context, r adapter.Request) (adapter.ListResult, error) {
	records, err := a.metadata(ctx)
	if err != nil {
		return adapter.ListResult{}, err
	}
	entries := make([]adapter.Entry, 0, len(records))
	for _, v := range records {
		entries = append(entries, v.Entry)
	}
	dir, _ := a.cwd()
	page, next, err := nativepage.Slice(a.Agent+"\x00"+dir, entries, r.Cursor, r.Limit)
	return adapter.ListResult{Entries: page, NextCursor: next}, err
}

func (a Adapter) show(ctx context.Context, name string) (nativeRecord, error) {
	args := []string{"--format", "json", a.Agent, "sessions", "show"}
	if name != "" {
		args = append(args, "--", name)
	}
	raw, err := a.run(ctx, "", adapter.MaxResponseBytes, args...)
	if err != nil {
		return nativeRecord{}, err
	}
	var rec nativeRecord
	if json.Unmarshal(raw, &rec) != nil || rec.ID == "" || rec.NativeID == "" || rec.Name != name {
		return nativeRecord{}, adapter.Fail("unavailable", "never", "acpx returned an incomplete session record")
	}
	cwd, err := a.cwd()
	if err != nil {
		return nativeRecord{}, err
	}
	if filepath.Clean(cwd) != filepath.Clean(rec.Cwd) {
		return nativeRecord{}, adapter.Fail("source_changed", "never", "acpx returned a session from another project")
	}
	return rec, nil
}
func visibleTurns(messages []json.RawMessage) ([]clean.Item, error) {
	out := make([]clean.Item, 0, len(messages))
	for _, raw := range messages {
		if string(raw) == `"Resume"` {
			continue
		}
		var m struct {
			User  *turn
			Agent *turn
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, adapter.Fail("unsupported_version", "never", "acpx returned an unknown conversation record")
		}
		if (m.User == nil) == (m.Agent == nil) {
			return nil, adapter.Fail("unsupported_version", "never", "acpx returned an unknown conversation role")
		}
		t, kind := m.User, "userMessage"
		if t == nil {
			t, kind = m.Agent, "agentMessage"
		}
		var texts []string
		for _, entry := range t.Content {
			if strings.TrimSpace(entry.Text) != "" {
				texts = append(texts, entry.Text)
			}
		}
		if len(texts) != 0 {
			out = append(out, clean.Item{Type: kind, Text: strings.Join(texts, "\n")})
		}
	}
	return out, nil
}

func (a Adapter) read(ctx context.Context, r adapter.Request) (adapter.ReadResult, error) {
	if strings.TrimSpace(r.Selection) == "" {
		return adapter.ReadResult{}, adapter.Fail("invalid_input", "never", "select an exact acpx record ID from beam list")
	}
	data, err := a.metadata(ctx)
	if err != nil {
		return adapter.ReadResult{}, err
	}
	for _, v := range data {
		if v.Selection != r.Selection {
			continue
		}
		rec, err := a.show(ctx, v.name)
		if err != nil {
			return adapter.ReadResult{}, err
		}
		if rec.ID != r.Selection {
			return adapter.ReadResult{}, adapter.Fail("source_changed", "never", "this record's name now selects another acpx session; select an active record")
		}
		items, err := visibleTurns(rec.Messages)
		if err != nil {
			return adapter.ReadResult{}, err
		}
		updated := rec.UpdatedAt
		if updated == "" {
			updated = rec.LastUsedAt
		}
		page, next, err := nativepage.Slice(rec.Cwd+"\x00"+rec.ID+"\x00"+updated, items, r.Cursor, r.Limit)
		if err != nil {
			return adapter.ReadResult{}, err
		}
		title := rec.Title
		if strings.TrimSpace(title) == "" {
			title = v.Title
		}
		// ACPX retains a bounded runtime conversation with no authoritative flag
		// for content discarded before this read.
		c := clean.Candidate{Source: "acpx", Origin: clean.Origin{Adapter: "acpx", SourceHome: rec.Cwd, NativeSession: rec.ID}, Title: title, UpdatedAt: updated, Completed: rec.Closed, Completeness: "partial", Items: page}
		return adapter.ReadResult{Candidate: c, NextCursor: next}, nil
	}
	return adapter.ReadResult{}, adapter.Fail("unavailable", "never", "the requested acpx record is unavailable in this project")
}
