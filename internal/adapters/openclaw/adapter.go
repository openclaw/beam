// Package openclaw exports an explicitly selected Gateway session under the current operator's authority.
package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/nativepage"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/openclawcli"
)

type Adapter struct{ ProfileDir, Endpoint, Executable string }
type source interface {
	SourceCall(context.Context, string, any) (json.RawMessage, error)
}

func (a Adapter) Call(ctx context.Context, r adapter.Request) (any, error) {
	if r.Op == "capabilities" {
		v, err := openclawcli.SourceVersion(ctx, a.Executable)
		if err != nil {
			return nil, adapter.Fail("unavailable", "never", err.Error())
		}
		return adapter.Capabilities{Protocol: 1, Adapter: "openclaw", Version: v, Source: []string{"list", "read"}, Destination: []string{}, Recovery: "manual"}, nil
	}
	if r.Op != "list" && r.Op != "read" {
		return nil, adapter.Unsupported(r.Op)
	}
	c, origin, err := openclawcli.NewSource(ctx, a.ProfileDir, a.Endpoint, a.Executable)
	if err != nil {
		return nil, err
	}
	if r.Op == "list" {
		return list(ctx, c, r)
	}
	return read(ctx, c, origin, r)
}

func list(ctx context.Context, c source, r adapter.Request) (adapter.ListResult, error) {
	offset := 0
	if r.Cursor != "" {
		n, err := strconv.Atoi(r.Cursor)
		if err != nil || n < 0 {
			return adapter.ListResult{}, errors.New("invalid list cursor")
		}
		offset = n
	}
	b, err := c.SourceCall(ctx, "sessions.list", map[string]any{"limit": r.Limit, "offset": offset, "rowMode": "compact"})
	if err != nil {
		return adapter.ListResult{}, err
	}
	var response struct {
		Sessions   []struct{ Key, DisplayName, Label string }
		NextOffset *int
		HasMore    bool
	}
	if err := json.Unmarshal(b, &response); err != nil || response.Sessions == nil {
		return adapter.ListResult{}, errors.New("invalid Gateway session list")
	}
	result := adapter.ListResult{Entries: []adapter.Entry{}}
	for _, s := range response.Sessions {
		if s.Key != "" {
			title := s.DisplayName
			if title == "" {
				title = s.Label
			}
			if title == "" {
				title = s.Key
			}
			result.Entries = append(result.Entries, adapter.Entry{Selection: s.Key, Title: title})
		}
	}
	if response.HasMore && response.NextOffset != nil && *response.NextOffset > offset {
		result.NextCursor = strconv.Itoa(*response.NextOffset)
	}
	return result, nil
}

type locator struct {
	CatalogID    string `json:"catalogId"`
	HostID       string `json:"hostId"`
	ThreadID     string `json:"threadId"`
	AgentID      string `json:"agentId,omitempty"`
	SourceHomeID string `json:"sourceHomeId,omitempty"`
}

func read(ctx context.Context, c source, home string, r adapter.Request) (adapter.ReadResult, error) {
	candidate := clean.Candidate{Source: "openclaw", Origin: clean.Origin{Adapter: "openclaw", SourceHome: home, NativeSession: r.Selection}, UpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), Title: "OpenClaw conversation", Completeness: "complete"}
	if strings.HasPrefix(strings.TrimSpace(r.Selection), "{") {
		var loc locator
		dec := json.NewDecoder(strings.NewReader(r.Selection))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&loc); err != nil || loc.CatalogID == "" || loc.HostID == "" || loc.ThreadID == "" {
			return adapter.ReadResult{}, errors.New("select a complete catalog locator")
		}
		normalized, _ := json.Marshal(loc)
		candidate.Origin.NativeSession = string(normalized)
		if err := catalogHistory(ctx, c, loc, &candidate); err != nil {
			return adapter.ReadResult{}, err
		}
	} else if err := chatHistory(ctx, c, r.Selection, &candidate); err != nil {
		return adapter.ReadResult{}, err
	}
	items, next, err := nativepage.Slice(candidate.Origin.SourceHome+"#"+candidate.Origin.NativeSession, candidate.Items, r.Cursor, r.Limit)
	candidate.Items = items
	return adapter.ReadResult{Candidate: candidate, NextCursor: next}, err
}

type sender struct{ Identity struct{ Type, ID string } }

func (s sender) key() string {
	if s.Identity.Type == "" || s.Identity.ID == "" {
		return ""
	}
	return s.Identity.Type + ":" + s.Identity.ID
}

func setTime(c *clean.Candidate, raw json.RawMessage) {
	var n int64
	var instant time.Time
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		instant = time.UnixMilli(n).UTC()
	} else {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			instant, _ = time.Parse(time.RFC3339Nano, v)
		}
	}
	prev, _ := time.Parse(time.RFC3339Nano, c.UpdatedAt)
	if !instant.IsZero() && instant.After(prev) {
		c.UpdatedAt = instant.UTC().Format(time.RFC3339Nano)
	}
}

func textParts(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct{ Type, Text string }
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

func chatHistory(ctx context.Context, c source, key string, dst *clean.Candidate) error {
	params := map[string]any{"sessionKey": key, "limit": 100, "maxBytes": 128 << 10}
	type message struct {
		Role      string
		Content   json.RawMessage
		Timestamp json.RawMessage
		Sender    sender
	}
	type page struct {
		SessionID            string
		Messages             []message
		HasMore, WindowReset bool
		OlderCursor          string
		NextOffset           *int
		TotalMessages        *int
	}
	seen := make(map[string]bool)
	var nativeID string
	total := -1
	for step := 0; step < 32; step++ {
		b, err := c.SourceCall(ctx, "chat.history", params)
		if err != nil {
			return err
		}
		var resp page
		if json.Unmarshal(b, &resp) != nil || resp.Messages == nil {
			return errors.New("invalid Gateway history page")
		}
		if resp.WindowReset || step > 0 && nativeID != "" && resp.SessionID != nativeID || step > 0 && total >= 0 && resp.TotalMessages != nil && *resp.TotalMessages != total {
			return adapter.Fail("source_changed", "never", "Gateway history changed while reading")
		}
		if step == 0 {
			nativeID = resp.SessionID
			if resp.TotalMessages != nil {
				total = *resp.TotalMessages
			}
			if nativeID != "" {
				dst.Origin.NativeSession = key + "#" + nativeID
			}
		}
		older := make([]clean.Item, 0, len(resp.Messages))
		for _, m := range resp.Messages {
			var kind string
			switch m.Role {
			case "user":
				kind = "userMessage"
			case "assistant":
				kind = "agentMessage"
			default:
				continue
			}
			txt := textParts(m.Content)
			if strings.TrimSpace(txt) == "" {
				continue
			}
			item := clean.Item{Type: kind, Text: txt}
			if m.Role == "user" {
				item.SpeakerID = m.Sender.key()
			}
			older = append(older, item)
			setTime(dst, m.Timestamp)
		}
		dst.Items = append(older, dst.Items...)
		if !resp.HasMore {
			return nil
		}
		if len(dst.Items) >= 200 || step == 31 {
			dst.Completeness = "partial"
			return nil
		}
		var mark string
		if resp.OlderCursor != "" {
			delete(params, "offset")
			mark = resp.OlderCursor
			params["cursor"] = mark
		} else if resp.NextOffset != nil {
			delete(params, "cursor")
			mark = strconv.Itoa(*resp.NextOffset)
			params["offset"] = *resp.NextOffset
		} else {
			dst.Completeness = "partial"
			return nil
		}
		if seen[mark] {
			return errors.New("Gateway history cursor repeated")
		}
		seen[mark] = true
	}
	return nil
}

func catalogHistory(ctx context.Context, c source, loc locator, dst *clean.Candidate) error {
	params := map[string]any{"catalogId": loc.CatalogID, "hostId": loc.HostID, "threadId": loc.ThreadID, "limit": 100}
	if loc.AgentID != "" {
		params["agentId"] = loc.AgentID
	}
	if loc.SourceHomeID != "" {
		params["sourceHomeId"] = loc.SourceHomeID
	}
	seen := map[string]bool{}
	for step := 0; step < 32; step++ {
		b, err := c.SourceCall(ctx, "sessions.catalog.read", params)
		if err != nil {
			return err
		}
		var resp struct {
			HostID, ThreadID, Label, NextCursor string
			Items                               []struct {
				Type, Text string
				Timestamp  json.RawMessage
				Truncated  bool
				Sender     sender
			}
		}
		if json.Unmarshal(b, &resp) != nil || resp.Items == nil || resp.HostID != loc.HostID || resp.ThreadID != loc.ThreadID {
			return fmt.Errorf("Gateway returned a different or invalid catalog page")
		}
		if resp.Label != "" {
			dst.Title = resp.Label
		}
		older := make([]clean.Item, 0, len(resp.Items))
		for _, i := range resp.Items {
			if i.Truncated {
				dst.Completeness = "partial"
			}
			if (i.Type != "agentMessage" && i.Type != "userMessage") || strings.TrimSpace(i.Text) == "" {
				continue
			}
			item := clean.Item{Type: i.Type, Text: i.Text}
			if i.Type == "userMessage" {
				item.SpeakerID = i.Sender.key()
			}
			older = append(older, item)
			setTime(dst, i.Timestamp)
		}
		slices.Reverse(older)
		dst.Items = append(older, dst.Items...)
		if resp.NextCursor == "" {
			return nil
		}
		if len(dst.Items) >= 200 || step == 31 {
			dst.Completeness = "partial"
			return nil
		}
		if seen[resp.NextCursor] {
			return errors.New("catalog cursor repeated")
		}
		seen[resp.NextCursor] = true
		params["cursor"] = resp.NextCursor
	}
	return nil
}
