// Package pi projects exact Pi session-tree ancestry and supplies a first-turn reference extension.
package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	native "github.com/openclaw/beam/integrations/pi"
	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/localhistory"
	"github.com/openclaw/beam/internal/adapters/staged"
)

type Adapter struct{ Project, AgentDir, ReferenceRoot string }

func (a Adapter) Call(ctx context.Context, q adapter.Request) (any, error) {
	return localhistory.Dispatch(ctx, q, staged.Store{Adapter: "pi", Root: a.ReferenceRoot, Prepare: native.Prepare, NextAction: func(dir string) string { return "pi -e " + staged.Quote(filepath.Join(dir, "reference.mjs")) }}, a.reader)
}

type header struct {
	Type, ID, Cwd string
	Version       int
}

func parseHeader(f *os.File) (header, error) {
	var h header
	first := bufio.NewReader(io.LimitReader(f, 64<<10))
	line, err := first.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return h, err
	}
	if err = json.Unmarshal(line, &h); err != nil {
		return h, errors.New("invalid Pi session header")
	}
	return h, nil
}
func (a Adapter) reader() (localhistory.Reader, error) {
	project, err := filepath.Abs(a.Project)
	if err != nil {
		return localhistory.Reader{}, err
	}
	if p, e := filepath.EvalSymlinks(project); e == nil {
		project = p
	}
	root := a.AgentDir
	if root == "" {
		root, err = localhistory.Home("PI_CODING_AGENT_DIR", filepath.Join(".pi", "agent"))
		if err != nil {
			return localhistory.Reader{}, err
		}
	}
	dir := filepath.Join(root, "sessions", "--"+strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(filepath.ToSlash(project), "/"))+"--")
	return localhistory.Reader{Name: "pi", SourceHome: dir, Files: func(ctx context.Context, wanted string) ([]localhistory.File, error) {
		names, err := localhistory.Names(dir, ".jsonl")
		if err != nil {
			return nil, err
		}
		var out []localhistory.File
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			f, err := localhistory.Open(dir, name)
			if err != nil {
				return nil, err
			}
			h, err := parseHeader(f)
			st, statErr := f.Stat()
			f.Close()
			// An unrelated invalid file should not hide other valid sessions; reading a selected file requires its own verified header.
			if err != nil || h.Type != "session" || h.ID == "" || filepath.Clean(h.Cwd) != project || wanted != "" && h.ID != wanted {
				continue
			}
			if statErr != nil {
				return nil, statErr
			}
			out = append(out, localhistory.File{ID: h.ID, Title: "Pi session " + h.ID, Path: filepath.Join(dir, name), Updated: st.ModTime()})
		}
		return out, nil
	}, Load: load}, nil
}

type node struct {
	Type, ID, ParentID, Timestamp string
	Message                       struct {
		Role    string
		Content json.RawMessage
	}
}

func load(ctx context.Context, file localhistory.File, branch string) (localhistory.History, error) {
	f, err := localhistory.Open(filepath.Dir(file.Path), filepath.Base(file.Path))
	if err != nil {
		return localhistory.History{}, err
	}
	defer f.Close()
	var nodes []localhistory.Node
	headerRead := false
	err = localhistory.Lines(ctx, f, func(data []byte) error {
		if !headerRead {
			var h header
			if json.Unmarshal(data, &h) != nil || h.Type != "session" || h.ID != file.ID {
				return errors.New("selected Pi session changed")
			}
			if h.Version != 2 && h.Version != 3 {
				return adapter.Fail("unsupported_version", "after_setup", "Pi reader supports session tree formats 2 and 3")
			}
			headerRead = true
			return nil
		}
		var r node
		if json.Unmarshal(data, &r) != nil || r.ID == "" {
			return errors.New("invalid Pi session tree entry")
		}
		n := localhistory.Node{ID: r.ID, Parent: r.ParentID, Time: r.Timestamp}
		if r.Type == "message" {
			n.Items = localhistory.Message(r.Message.Role, localhistory.Visible(r.Message.Content))
		}
		nodes = append(nodes, n)
		return nil
	})
	if err != nil {
		return localhistory.History{}, err
	}
	h, err := localhistory.Branch(nodes, branch)
	for _, it := range h.Items {
		if it.Type == "userMessage" {
			h.Title = it.Text
			break
		}
	}
	return h, err
}
