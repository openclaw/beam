// Package codex transfers selected native Codex threads through its stdio app-server.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/native"
	"github.com/openclaw/beam/internal/adapters/nativepage"
	"github.com/openclaw/beam/internal/clean"
)

type Adapter struct{}

const bin = "codex"

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type block = native.Block
type turn struct {
	ID, Status, ItemsView string
	Items                 []struct {
		Type, Text string
		Content    []block
	}
}
type thread struct {
	ID, Name, Preview, Path string
	UpdatedAt               int64
	Ephemeral               bool
	Turns                   []turn
}
type reply struct{ Thread thread }

func (Adapter) Call(ctx context.Context, r adapter.Request) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	versionCtx, done := context.WithTimeout(ctx, 5*time.Second)
	output, err := exec.CommandContext(versionCtx, bin, "--version").Output()
	done()
	if err != nil {
		return nil, adapter.Fail("unavailable", "never", "install Codex CLI to use this adapter")
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 || fields[0] != "codex-cli" || fields[1] != "0.172.0" {
		return nil, adapter.Fail("unsupported", "never", "this Beam adapter requires Codex CLI 0.172.0")
	}
	if r.Op == "capabilities" {
		return adapter.Capabilities{Protocol: 1, Adapter: bin, Version: fields[1], Recovery: "manual", Source: []string{"list", "read"}, Destination: []string{"openCopy", "resolve", "send"}}, nil
	}
	switch r.Op {
	case "list", "openCopy":
	case "read":
		if !idPattern.MatchString(r.Selection) {
			return nil, adapter.Fail("invalid_input", "never", "select an exact Codex thread ID")
		}
	case "resolve", "send":
		if !idPattern.MatchString(r.Handle) {
			return nil, adapter.Fail("invalid_input", "never", "use a native Codex thread handle")
		}
	default:
		return nil, adapter.Fail("unsupported", "never", "unsupported Codex operation")
	}
	if r.Op == "send" && (r.RequestID == "" || strings.TrimSpace(r.Instruction) == "") {
		return nil, adapter.Fail("invalid_input", "never", "sending requires a request ID and a new instruction")
	}
	c, err := start(ctx, bin)
	if err != nil {
		return nil, adapter.Fail("unavailable", "never", "could not initialize the local Codex app-server")
	}
	defer c.close()
	switch r.Op {
	case "list":
		var page struct {
			Data       []thread
			NextCursor string
		}
		limit := r.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 200 {
			return nil, adapter.Fail("invalid_input", "never", "page limit must be between 1 and 200")
		}
		p := obj{"limit": limit}
		if r.Cursor != "" {
			p["cursor"] = r.Cursor
		}
		if err := c.call("thread/list", p, &page); err != nil {
			return nil, adapter.Fail("unavailable", "never", "Codex could not list threads")
		}
		rows := make([]adapter.Entry, 0, len(page.Data))
		for _, t := range page.Data {
			rows = append(rows, adapter.Entry{Selection: t.ID, Title: t.title()})
		}
		return adapter.ListResult{Entries: rows, NextCursor: page.NextCursor}, nil
	case "openCopy":
		if r.OperationID == "" {
			return nil, adapter.Fail("invalid_input", "never", "opening a copy requires an operation ID")
		}
		return openCopy(ctx, c, r)
	case "send":
		return send(c, r)
	default:
		id := r.Selection
		if r.Op == "resolve" {
			id = r.Handle
		}
		var got reply
		if err := c.call("thread/read", obj{"threadId": id, "includeTurns": r.Op == "read"}, &got); err != nil || got.Thread.ID != id || got.Thread.Ephemeral {
			return nil, adapter.Fail("unavailable", "never", "the selected persisted Codex thread is unavailable")
		}
		if r.Op == "resolve" {
			return ready(id), nil
		}
		return read(got.Thread, r)
	}
}
func (t thread) title() string {
	if strings.TrimSpace(t.Name) != "" {
		return t.Name
	}
	if strings.TrimSpace(t.Preview) != "" {
		return t.Preview
	}
	return "Codex conversation"
}
func ready(id string) adapter.OpenResult {
	return adapter.OpenResult{State: "ready", Handle: id, NativeSession: id, NextAction: "Open with codex resume " + id + ". Imported history is model-visible context and does not appear as earlier UI turns."}
}
func uncertain(id, handle string) adapter.OpenResult {
	return adapter.OpenResult{State: "needs_reconciliation", OperationID: id, Handle: handle, NextAction: "Inspect the native Codex thread before creating another copy."}
}

func openCopy(ctx context.Context, c *client, r adapter.Request) (any, error) {
	var created reply
	if err := c.call("thread/start", obj{"ephemeral": false}, &created); err != nil || !idPattern.MatchString(created.Thread.ID) {
		return uncertain(r.OperationID, ""), nil
	}
	id := created.Thread.ID
	wanted := native.Frame(r.Packet)
	if err := c.call("thread/inject_items", obj{"threadId": id, "items": []obj{{"type": "message", "role": "user", "content": wanted}}}, nil); err != nil {
		return uncertain(r.OperationID, id), nil
	}
	if err := c.call("thread/name/set", obj{"threadId": id, "name": r.Packet.Title}, nil); err != nil {
		return uncertain(r.OperationID, id), nil
	}
	// Reopen through a separate app-server process. An in-memory ID can exist
	// before the rollout file does; the raw injected reference is absent from UI turns.
	c.close()
	probe, err := start(ctx, bin)
	if err != nil {
		return uncertain(r.OperationID, id), nil
	}
	defer probe.close()
	var persisted reply
	if err := probe.call("thread/resume", obj{"threadId": id}, &persisted); err != nil || persisted.Thread.ID != id || persisted.Thread.Ephemeral {
		return uncertain(r.OperationID, id), nil
	}
	got := persistedReference(persisted.Thread)
	if !reflect.DeepEqual(wanted, got) {
		return uncertain(r.OperationID, id), nil
	}
	return ready(id), nil
}

func persistedReference(t thread) []block {
	if !idPattern.MatchString(t.ID) || !strings.HasSuffix(filepath.Base(t.Path), "-"+t.ID+".jsonl") {
		return nil
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// Beam injects exactly one framed reference before the first native turn.
	// Never interpret arbitrary response items, developer guidance or tool output.
	s := bufio.NewScanner(io.LimitReader(f, 4<<20))
	s.Buffer(make([]byte, 16384), 4<<20)
	for s.Scan() {
		var row struct {
			Type    string
			Payload struct {
				Type, Role string
				Content    []block
			}
		}
		if json.Unmarshal(s.Bytes(), &row) != nil {
			continue
		}
		if row.Type == "event_msg" && row.Payload.Type == "task_started" {
			return nil
		}
		v := row.Payload
		if row.Type == "response_item" && v.Type == "message" && v.Role == "user" && len(v.Content) > 1 && v.Content[0] == (block{Type: "input_text", Text: native.ReferenceHeader}) {
			return v.Content
		}
	}
	return nil
}
func read(t thread, r adapter.Request) (any, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return nil, e
		}
		home = filepath.Join(h, ".codex")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	res := clean.Candidate{Source: bin, Origin: clean.Origin{Adapter: bin, SourceHome: home, NativeSession: t.ID}, Title: t.title(), UpdatedAt: time.Unix(t.UpdatedAt, 0).UTC().Format(time.RFC3339), Completeness: "complete"}
	res.Items = append(res.Items, native.ReferenceItems(persistedReference(t))...)
	for _, turn := range t.Turns {
		if turn.ItemsView == "notLoaded" || turn.ItemsView == "summary" {
			res.Completeness = "partial"
		}
		res.Completed = turn.Status == "completed"
		for _, it := range turn.Items {
			switch it.Type {
			case "userMessage":
				for _, b := range it.Content {
					switch b.Type {
					case "text":
						res.Items = append(res.Items, clean.Item{Type: "userMessage", Text: b.Text})
					case "image", "localImage":
						res.Items = append(res.Items, clean.Item{Type: "other", Text: "[image omitted]"})
					}
				}
			case "agentMessage":
				res.Items = append(res.Items, clean.Item{Type: "agentMessage", Text: it.Text})
			}
		}
	}
	page, next, err := nativepage.Slice(t.ID+" "+res.UpdatedAt, res.Items, r.Cursor, r.Limit)
	if err != nil {
		return nil, err
	}
	res.Items = page
	return adapter.ReadResult{Candidate: res, NextCursor: next}, nil
}
func send(c *client, r adapter.Request) (any, error) {
	var got reply
	if err := c.call("thread/resume", obj{"threadId": r.Handle}, &got); err != nil || got.Thread.ID != r.Handle {
		return nil, adapter.Fail("unavailable", "never", "the selected Codex thread could not be resumed")
	}
	var started struct{ Turn turn }
	if err := c.call("turn/start", obj{"threadId": r.Handle, "input": []block{{Type: "text", Text: r.Instruction}}}, &started); err != nil {
		return nil, sendUncertain()
	}
	for {
		ev, err := c.next()
		if err != nil {
			return nil, sendUncertain()
		}
		if ev.Method != "turn/completed" {
			continue
		}
		var result struct {
			ThreadID string
			Turn     turn
		}
		if json.Unmarshal(ev.Params, &result) != nil || result.ThreadID != r.Handle || result.Turn.ID != started.Turn.ID {
			continue
		}
		if result.Turn.Status != "completed" {
			return nil, adapter.Fail("turn_failed", "never", fmt.Sprintf("Codex reports the selected turn ended with status %q; inspect it before sending again", result.Turn.Status))
		}
		return adapter.SendResult{State: "completed", RequestID: r.RequestID, Handle: r.Handle}, nil
	}
}
func sendUncertain() error {
	return adapter.Fail("needs_reconciliation", "after_reconcile", "inspect the Codex thread before resending the instruction")
}
