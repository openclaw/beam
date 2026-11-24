// Package gemini reads an explicitly selected Gemini export or project session.
package gemini

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	native "github.com/openclaw/beam/integrations/gemini"
	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/localhistory"
	"github.com/openclaw/beam/internal/adapters/staged"
)

type Adapter struct{ Project, ConfigDir, ReferenceRoot string }

func (a Adapter) Call(ctx context.Context, q adapter.Request) (any, error) {
	if q.Op == "read" && strings.HasPrefix(q.Selection, "export:") {
		return explicitExport(ctx, q)
	}
	return localhistory.Dispatch(ctx, q, staged.Store{Adapter: "gemini", Root: a.ReferenceRoot, Prepare: native.Prepare, NextAction: func(dir string) string {
		return "gemini --session-file " + staged.Quote(filepath.Join(dir, "reference.gemini.json"))
	}}, a.reader)
}
func (a Adapter) reader() (localhistory.Reader, error) {
	project, err := filepath.Abs(a.Project)
	if err != nil {
		return localhistory.Reader{}, err
	}
	// Gemini hashes the native process.cwd() spelling. Windows folds case only for
	// projects.json lookup; resolving symlinks there can change the hash input.
	if runtime.GOOS != "windows" {
		if p, e := filepath.EvalSymlinks(project); e == nil {
			project = p
		}
	}
	home := a.ConfigDir
	if home == "" {
		base := os.Getenv("GEMINI_CLI_HOME")
		if base == "" {
			base, err = os.UserHomeDir()
			if err != nil {
				return localhistory.Reader{}, err
			}
		}
		home = filepath.Join(base, ".gemini")
	}
	hash := sha256.Sum256([]byte(project))
	projectHash := hex.EncodeToString(hash[:])
	dirs := []string{projectHash}
	if registry, e := localhistory.Open(home, "projects.json"); e == nil {
		defer registry.Close()
		raw, err := io.ReadAll(io.LimitReader(registry, 4<<20))
		if err != nil {
			return localhistory.Reader{}, err
		}
		var r struct{ Projects map[string]string }
		if json.Unmarshal(raw, &r) != nil {
			return localhistory.Reader{}, errors.New("invalid Gemini project registry")
		}
		key := project
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if slug := r.Projects[key]; slug != "" && slug != projectHash {
			if filepath.Base(slug) != slug || slug == "." || slug == ".." {
				return localhistory.Reader{}, errors.New("invalid Gemini project directory")
			}
			dirs = append(dirs, slug)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return localhistory.Reader{}, e
	}
	return localhistory.Reader{Name: "gemini", SourceHome: filepath.Join(home, "tmp", projectHash), Files: func(ctx context.Context, wanted string) ([]localhistory.File, error) {
		var files []localhistory.File
		for _, d := range dirs {
			dir := filepath.Join(home, "tmp", d, "chats")
			names, err := localhistory.Names(dir, ".jsonl", ".json")
			if err != nil {
				return nil, err
			}
			for _, name := range names {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !strings.HasPrefix(name, "session-") {
					continue
				}
				f, err := localhistory.Open(dir, name)
				if err != nil {
					return nil, err
				}
				var m metadata
				dec := json.NewDecoder(bufio.NewReader(io.LimitReader(f, localhistory.MaxSourceBytes+1)))
				// Decode only metadata keys; JSONL headers precede conversation records.
				err = dec.Decode(&m)
				st, statErr := f.Stat()
				f.Close()
				if err != nil || m.SessionID == "" || m.ProjectHash != projectHash || m.Kind == "subagent" || wanted != "" && wanted != m.SessionID {
					continue
				}
				if statErr != nil {
					return nil, statErr
				}
				files = append(files, localhistory.File{ID: m.SessionID, Title: "Gemini session " + m.SessionID, Path: filepath.Join(dir, name), Updated: st.ModTime()})
			}
		}
		return files, nil
	}, Load: func(ctx context.Context, file localhistory.File, branch string) (localhistory.History, error) {
		if branch != "" {
			return localhistory.History{}, errors.New("Gemini sessions have no selectable branch")
		}
		f, err := localhistory.Open(filepath.Dir(file.Path), filepath.Base(file.Path))
		if err != nil {
			return localhistory.History{}, err
		}
		defer f.Close()
		return parseNative(ctx, f, file.ID, projectHash)
	}}, nil
}

type metadata struct{ SessionID, ProjectHash, LastUpdated, Kind string }
type message struct {
	ID, Type, Timestamp     string
	Content, DisplayContent json.RawMessage
}

func txt(raw json.RawMessage) string {
	var result []string
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		parts = []json.RawMessage{raw}
	}
	for _, p := range parts {
		var s string
		if json.Unmarshal(p, &s) != nil {
			var v struct {
				Text                                                 string
				Thought                                              bool
				FunctionCall, FunctionResponse, InlineData, FileData json.RawMessage
			}
			if json.Unmarshal(p, &v) != nil || v.Thought || len(v.FunctionCall)+len(v.FunctionResponse)+len(v.InlineData)+len(v.FileData) > 0 {
				continue
			}
			s = v.Text
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" || strings.Contains(trimmed, "<session_context>") || strings.Contains(trimmed, "<hook_context>") {
			continue
		}
		result = append(result, s)
	}
	return strings.Join(result, "\n")
}
func nativeItems(role string, content json.RawMessage) []localhistory.Item {
	if role == "gemini" {
		role = "model"
	}
	return localhistory.Message(role, txt(content))
}
func explicitExport(ctx context.Context, q adapter.Request) (any, error) {
	name := strings.TrimPrefix(q.Selection, "export:")
	if name == "" {
		return nil, errors.New("provide export:<path to Gemini JSON export>")
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	idHash := sha256.Sum256([]byte(path))
	id := hex.EncodeToString(idHash[:16])
	q.Selection = id
	reader := localhistory.Reader{Name: "gemini", SourceHome: filepath.Dir(path), Files: func(ctx context.Context, _ string) ([]localhistory.File, error) {
		f, err := localhistory.Open(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		return []localhistory.File{{ID: id, Path: path, Title: "Gemini conversation", Updated: st.ModTime()}}, nil
	}, Load: func(ctx context.Context, _ localhistory.File, _ string) (localhistory.History, error) {
		var h localhistory.History
		f, err := localhistory.Open(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return h, err
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, localhistory.MaxSourceBytes+1))
		if err != nil {
			return h, err
		}
		if len(raw) > localhistory.MaxSourceBytes {
			return h, errors.New("Gemini export exceeds local read budget")
		}
		var rows []struct {
			Role  string
			Parts json.RawMessage
		}
		if json.Unmarshal(raw, &rows) != nil {
			return h, errors.New("expected a Gemini /chat share JSON export")
		}
		h.Partial = true // This is the harness's exported model history and may already be compacted.
		for _, r := range rows {
			if err := ctx.Err(); err != nil {
				return h, err
			}
			if r.Role != "model" && r.Role != "user" {
				continue
			}
			h.Items = append(h.Items, nativeItems(r.Role, r.Parts)...)
		}
		for _, it := range h.Items {
			if it.Type == "userMessage" {
				h.Title = it.Text
				break
			}
		}
		return h, nil
	}}
	return reader.Call(ctx, q)
}
func parseNative(ctx context.Context, f *os.File, id, project string) (localhistory.History, error) {
	var h localhistory.History
	active := map[string]message{}
	var order []string
	seenHeader := false
	add := func(m message) {
		if m.ID == "" {
			return
		}
		if _, ok := active[m.ID]; !ok {
			order = append(order, m.ID)
		}
		active[m.ID] = m
	}
	consume := func(raw []byte) error {
		var m struct {
			metadata
			message
			Messages []message
			Rewind   *string `json:"$rewindTo"`
			Set      *struct {
				LastUpdated string
				Messages    []message
			} `json:"$set"`
			Patch *struct {
				ID      string
				Content json.RawMessage
				Updates []struct {
					ID      string
					Content json.RawMessage
				}
				RemoveIDs, OrderIDs []string
			} `json:"$patch"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return errors.New("malformed Gemini session; use an explicit /chat share JSON export")
		}
		if m.SessionID != "" {
			if m.SessionID != id || m.ProjectHash != project || m.Kind == "subagent" {
				return errors.New("Gemini session identity changed")
			}
			seenHeader = true
		}
		if !seenHeader {
			return errors.New("Gemini session metadata is missing")
		}
		if m.LastUpdated != "" {
			h.UpdatedAt = m.LastUpdated
		}
		switch {
		case m.Rewind != nil:
			i := 0
			for j, v := range order {
				if v == *m.Rewind {
					i = j
					break
				}
			}
			for _, k := range order[i:] {
				delete(active, k)
			}
			order = order[:i]
		case m.Patch != nil:
			p := m.Patch
			update := func(id string, content json.RawMessage) {
				if r, ok := active[id]; ok && content != nil {
					r.Content = content
					active[id] = r
				}
			}
			update(p.ID, p.Content)
			for _, u := range p.Updates {
				update(u.ID, u.Content)
			}
			tail := map[string]bool{}
			for _, k := range p.RemoveIDs {
				delete(active, k)
			}
			for _, k := range p.OrderIDs {
				tail[k] = true
			}
			kept := order[:0]
			for _, k := range order {
				if _, ok := active[k]; ok && !tail[k] {
					kept = append(kept, k)
				}
			}
			for _, k := range p.OrderIDs {
				if _, ok := active[k]; ok && tail[k] {
					kept = append(kept, k)
					delete(tail, k)
				}
			}
			order = kept
		case m.Set != nil:
			if m.Set.LastUpdated != "" {
				h.UpdatedAt = m.Set.LastUpdated
			}
			if m.Set.Messages != nil {
				clear(active)
				order = nil
				for _, v := range m.Set.Messages {
					add(v)
				}
			}
		case m.ID != "":
			add(m.message)
		case m.SessionID != "":
			for _, v := range m.Messages {
				add(v)
			}
		default:
			return errors.New("unknown Gemini history operation; use an explicit /chat share JSON export")
		}
		return nil
	}
	var err error
	if strings.HasSuffix(f.Name(), ".json") {
		raw, e := io.ReadAll(io.LimitReader(f, localhistory.MaxSourceBytes+1))
		if e != nil {
			return h, e
		}
		if len(raw) > localhistory.MaxSourceBytes {
			return h, fmt.Errorf("Gemini session exceeds %d bytes", localhistory.MaxSourceBytes)
		}
		err = consume(bytes.TrimSpace(raw))
	} else {
		err = localhistory.Lines(ctx, f, consume)
	}
	if err != nil {
		return h, err
	}
	for _, k := range order {
		m := active[k]
		if m.Type != "user" && m.Type != "gemini" {
			continue
		}
		content := m.Content
		if len(m.DisplayContent) > 0 {
			content = m.DisplayContent
		}
		h.Items = append(h.Items, nativeItems(m.Type, content)...)
	}
	for _, it := range h.Items {
		if it.Type == "userMessage" {
			h.Title = it.Text
			break
		}
	}
	return h, nil
}
