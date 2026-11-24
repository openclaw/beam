// Package opencode integrates with the stable OpenCode v1.19.6 session API.
package opencode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/native"
	"github.com/openclaw/beam/internal/adapters/nativepage"
	"github.com/openclaw/beam/internal/clean"
)

type Adapter struct{}
type obj = map[string]any

var ids = regexp.MustCompile(`^ses_[a-zA-Z0-9]{1,96}$`)

type session struct {
	ID, Title string
	Time      struct{ Updated int64 }
}
type part struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic,omitempty"`
	Ignored   bool   `json:"ignored,omitempty"`
}
type message struct {
	Info struct {
		Role, ID, SessionID, Finish string
		Error                       json.RawMessage
		Time                        struct{ Completed int64 }
	}
	Parts []part
}

func (Adapter) Call(ctx context.Context, r adapter.Request) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	probe, done := context.WithTimeout(ctx, 10*time.Second)
	output, err := exec.CommandContext(probe, "opencode", "--version").Output()
	done()
	if err != nil {
		return nil, adapter.Fail("unavailable", "never", "install OpenCode CLI to use this adapter")
	}
	version := strings.TrimSpace(string(output))
	if version != "1.19.6" {
		return nil, adapter.Fail("unsupported_version", "never", "this Beam adapter requires OpenCode 1.19.6")
	}
	if r.Op == "capabilities" {
		return adapter.Capabilities{Protocol: 1, Adapter: "opencode", Version: version, Source: []string{"list", "read"}, Destination: []string{"openCopy", "resolve", "send"}, Recovery: "manual"}, nil
	}
	switch r.Op {
	case "list", "openCopy":
	case "read":
		if !ids.MatchString(r.Selection) {
			return nil, adapter.Fail("invalid_input", "never", "select an exact OpenCode session ID")
		}
	case "resolve", "send":
		if !ids.MatchString(r.Handle) {
			return nil, adapter.Fail("invalid_input", "never", "use a native OpenCode session handle")
		}
	default:
		return nil, adapter.Unsupported(r.Op)
	}
	if r.Op == "send" && (r.RequestID == "" || strings.TrimSpace(r.Instruction) == "") {
		return nil, adapter.Fail("invalid_input", "never", "sending requires a request ID and a new instruction")
	}
	s, err := start(ctx)
	if err != nil {
		return nil, adapter.Fail("unavailable", "never", "could not start a private local OpenCode server")
	}
	defer s.close()
	switch r.Op {
	case "list":
		// v1.19.6 has a size parameter but no continuation token on /session.
		// Page a bounded content-identified projection and reject an oversized list.
		var sessions []session
		if _, err = s.request(ctx, "GET", "/session?limit=2147483647", nil, &sessions); err != nil {
			return nil, adapter.Fail("unavailable", "never", "could not list sessions within the local response budget; select an exact ID")
		}
		rows := make([]adapter.Entry, 0, len(sessions))
		for _, s := range sessions {
			rows = append(rows, adapter.Entry{Selection: s.ID, Title: s.Title})
		}
		items, next, err := nativepage.Slice("opencode/list", rows, r.Cursor, r.Limit)
		if err != nil {
			return nil, err
		}
		return adapter.ListResult{Entries: items, NextCursor: next}, nil
	case "openCopy":
		if r.OperationID == "" {
			return nil, adapter.Fail("invalid_input", "never", "opening a copy requires an operation ID")
		}
		return open(ctx, s, r)
	case "read", "resolve":
		id := r.Handle
		if r.Op == "read" {
			id = r.Selection
		}
		var got session
		if _, err = s.request(ctx, "GET", "/session/"+id, nil, &got); err != nil || got.ID != id {
			return nil, adapter.Fail("unavailable", "never", "the selected OpenCode session is unavailable")
		}
		if r.Op == "resolve" {
			return ready(id), nil
		}
		return read(ctx, s, got, r)
	case "send":
		var msg message
		_, err = s.request(ctx, "POST", "/session/"+r.Handle+"/message", obj{"parts": []part{{Type: "text", Text: r.Instruction}}}, &msg)
		if err != nil || msg.Info.SessionID != r.Handle || msg.Info.Role != "assistant" {
			return nil, adapter.Fail("needs_reconciliation", "after_reconcile", "inspect the OpenCode session before resending the instruction")
		}
		if len(msg.Info.Error) > 0 && string(msg.Info.Error) != "null" {
			return nil, adapter.Fail("turn_failed", "never", "OpenCode reported a failed turn; inspect its session")
		}
		return adapter.SendResult{State: "completed", RequestID: r.RequestID, Handle: r.Handle}, nil
	}
	return nil, adapter.Unsupported(r.Op)
}
func ready(id string) adapter.OpenResult {
	return adapter.OpenResult{State: "ready", Handle: id, NativeSession: id, NextAction: "Open with opencode --session " + id}
}
func uncertain(op, id string) adapter.OpenResult {
	return adapter.OpenResult{State: "needs_reconciliation", OperationID: op, Handle: id, NextAction: "Inspect the native OpenCode session before creating another copy."}
}

func open(ctx context.Context, s *server, r adapter.Request) (any, error) {
	var created session
	if _, err := s.request(ctx, "POST", "/session", obj{"title": r.Packet.Title}, &created); err != nil || !ids.MatchString(created.ID) {
		return uncertain(r.OperationID, ""), nil
	}
	id := created.ID
	parts := make([]part, 0, len(r.Packet.Items)+1)
	for _, b := range native.Frame(r.Packet) {
		parts = append(parts, part{Type: "text", Text: b.Text})
	}
	var saved message
	if _, err := s.request(ctx, "POST", "/session/"+id+"/message", obj{"noReply": true, "parts": parts}, &saved); err != nil || saved.Info.Role != "user" || saved.Info.ID == "" {
		return uncertain(r.OperationID, id), nil
	}
	s.close()
	// Prove persistence across a real native process boundary. Check every
	// staged block, since an empty destination also has a valid native ID.
	probe, err := start(ctx)
	if err != nil {
		return uncertain(r.OperationID, id), nil
	}
	defer probe.close()
	var got message
	if _, err = probe.request(ctx, "GET", "/session/"+id+"/message/"+saved.Info.ID, nil, &got); err != nil || got.Info.SessionID != id || got.Info.Role != "user" || len(got.Parts) != len(parts) {
		return uncertain(r.OperationID, id), nil
	}
	for i, p := range got.Parts {
		if p.Type != "text" || p.Text != parts[i].Text {
			return uncertain(r.OperationID, id), nil
		}
	}
	return ready(id), nil
}
func read(ctx context.Context, s *server, t session, r adapter.Request) (any, error) {
	home := os.Getenv("OPENCODE_DB")
	if home == "" {
		home = os.Getenv("XDG_DATA_HOME")
		if home == "" {
			root, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			home = filepath.Join(root, ".local", "share")
		}
		home = filepath.Join(home, "opencode")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	candidate := clean.Candidate{Source: "opencode", Origin: clean.Origin{Adapter: "opencode", SourceHome: home, NativeSession: t.ID}, Title: t.Title, UpdatedAt: time.UnixMilli(t.Time.Updated).UTC().Format(time.RFC3339Nano), Completeness: "complete"}
	var messages []message
	h, err := s.request(ctx, "GET", "/session/"+t.ID+"/message?limit=200", nil, &messages)
	if err != nil {
		return nil, adapter.Fail("unavailable", "never", "could not read the selected OpenCode messages within the local response budget")
	}
	if h.Get("X-Next-Cursor") != "" {
		candidate.Completeness = "partial"
	}
	for _, msg := range messages {
		if msg.Info.SessionID != t.ID || (msg.Info.Role != "user" && msg.Info.Role != "assistant") {
			continue
		}
		candidate.Completed = msg.Info.Role == "assistant" && msg.Info.Time.Completed > 0 && (len(msg.Info.Error) == 0 || string(msg.Info.Error) == "null")
		var blocks []native.Block
		for _, p := range msg.Parts {
			if p.Type == "text" && !p.Synthetic && !p.Ignored {
				blocks = append(blocks, native.Block{Type: "text", Text: p.Text})
			}
		}
		if msg.Info.Role == "user" {
			if rows := native.ReferenceItems(blocks); rows != nil {
				candidate.Items = append(candidate.Items, rows...)
				continue
			}
		}
		kind := "userMessage"
		if msg.Info.Role == "assistant" {
			kind = "agentMessage"
		}
		for _, p := range msg.Parts {
			if p.Synthetic || p.Ignored {
				continue
			}
			switch p.Type {
			case "text":
				candidate.Items = append(candidate.Items, clean.Item{Type: kind, Text: p.Text})
			case "file":
				candidate.Items = append(candidate.Items, clean.Item{Type: "other", Text: "[attachment omitted]"})
			}
		}
	}
	items, next, err := nativepage.Slice(t.ID+" "+candidate.UpdatedAt, candidate.Items, r.Cursor, r.Limit)
	if err != nil {
		return nil, err
	}
	candidate.Items = items
	return adapter.ReadResult{Candidate: candidate, NextCursor: next}, nil
}
