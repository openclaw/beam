// Package claude reads a selected main Claude Code conversation and stages a passive session hook.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	native "github.com/openclaw/beam/integrations/claude"
	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/localhistory"
	"github.com/openclaw/beam/internal/adapters/staged"
)

type Adapter struct{ Project, ConfigDir, ReferenceRoot string }

func (a Adapter) Call(ctx context.Context, q adapter.Request) (any, error) {
	return localhistory.Dispatch(ctx, q, staged.Store{Adapter: "claude", Root: a.ReferenceRoot, Prepare: native.Prepare, NextAction: func(d string) string { return "claude --plugin-dir " + staged.Quote(filepath.Join(d, "bridge")) }}, a.reader)
}

var unsafeDirChars = regexp.MustCompile(`[^A-Za-z0-9]`)

func (a Adapter) reader() (localhistory.Reader, error) {
	home := a.ConfigDir
	var err error
	if home == "" {
		home, err = localhistory.Home("CLAUDE_CONFIG_DIR", ".claude")
		if err != nil {
			return localhistory.Reader{}, err
		}
	}
	cwd, err := filepath.Abs(a.Project)
	if err != nil {
		return localhistory.Reader{}, err
	}
	if resolved, e := filepath.EvalSymlinks(cwd); e == nil {
		cwd = resolved
	}
	name := unsafeDirChars.ReplaceAllString(cwd, "-")
	if override := os.Getenv("CLAUDE_CODE_PROJECT_DIR_NAME"); override != "" {
		if filepath.Base(override) != override || override == "." || override == ".." {
			return localhistory.Reader{}, errors.New("invalid configured Claude project directory name")
		}
		name = override
	}
	base := filepath.Join(home, "projects")
	return localhistory.Reader{Name: "claude", SourceHome: filepath.Join(base, name), Files: func(ctx context.Context, wanted string) ([]localhistory.File, error) {
		dirs := []string{name}
		if len(name) > 200 {
			entries, err := os.ReadDir(base)
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			dirs = nil
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), name[:200]) {
					dirs = append(dirs, e.Name())
				}
			}
		}
		var files []localhistory.File
		for _, d := range dirs {
			dir := filepath.Join(base, d)
			names, err := localhistory.Names(dir, ".jsonl")
			if err != nil {
				return nil, err
			}
			for _, n := range names {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				id := strings.TrimSuffix(n, ".jsonl")
				if wanted != "" && id != wanted {
					continue
				}
				if _, _, err := localhistory.Selection(id); err != nil || strings.Contains(id, "#") {
					continue
				}
				f, err := localhistory.Open(dir, n)
				if err != nil {
					return nil, err
				}
				caller, err := project(f)
				st, statErr := f.Stat()
				f.Close()
				if err != nil {
					return nil, err
				}
				if statErr != nil {
					return nil, statErr
				}
				if filepath.Clean(caller) != cwd {
					continue
				}
				files = append(files, localhistory.File{ID: id, Title: "Claude session " + id, Path: filepath.Join(dir, n), Updated: st.ModTime()})
			}
		}
		return files, nil
	}, Load: load}, nil
}
func project(f *os.File) (string, error) {
	s := bufio.NewScanner(io.LimitReader(f, 4<<20))
	s.Buffer(make([]byte, 32<<10), 4<<20)
	for s.Scan() {
		var r struct {
			Cwd         string
			IsSidechain bool
		}
		if json.Unmarshal(s.Bytes(), &r) == nil && !r.IsSidechain && r.Cwd != "" {
			return r.Cwd, nil
		}
	}
	return "", s.Err()
}

type row struct {
	Type, Subtype, UUID, ParentUUID, LogicalParentUUID, SessionID, Timestamp, AgentID string
	IsSidechain, IsMeta, IsCompactSummary                                             bool
	Message                                                                           struct {
		Role                     string
		Content                  json.RawMessage
		IsMeta, IsCompactSummary bool
	}
}

func load(ctx context.Context, file localhistory.File, branch string) (localhistory.History, error) {
	f, err := localhistory.Open(filepath.Dir(file.Path), filepath.Base(file.Path))
	if err != nil {
		return localhistory.History{}, err
	}
	defer f.Close()
	var nodes []localhistory.Node
	var latest string
	partial := false
	err = localhistory.Lines(ctx, f, func(data []byte) error {
		var r row
		if err := json.Unmarshal(data, &r); err != nil {
			return errors.New("malformed Claude transcript entry")
		}
		if r.IsSidechain || r.AgentID != "" || r.UUID == "" || (r.SessionID != "" && r.SessionID != file.ID) {
			return nil
		}
		n := localhistory.Node{ID: r.UUID, Parent: r.ParentUUID, Time: r.Timestamp}
		if r.Type == "system" && r.Subtype == "compact_boundary" {
			if n.Parent == "" {
				n.Parent = r.LogicalParentUUID
			}
			partial = true
		}
		if !r.IsMeta && !r.IsCompactSummary && !r.Message.IsMeta && !r.Message.IsCompactSummary &&
			(r.Type == "user" || r.Type == "assistant") && r.Message.Role == r.Type {
			n.Items = localhistory.Message(r.Type, localhistory.Visible(r.Message.Content))
			latest = r.UUID
		}
		nodes = append(nodes, n)
		return nil
	})
	if err != nil {
		return localhistory.History{}, err
	}
	if branch == "" {
		branch = latest
	}
	h, err := localhistory.Branch(nodes, branch)
	h.Partial = h.Partial || partial
	if err == nil {
		for _, it := range h.Items {
			if it.Type == "userMessage" {
				h.Title = it.Text
				break
			}
		}
	}
	return h, err
}
