package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/file"
)

type Registry map[string]adapter.Adapter

type App struct {
	OpenClaw func(profileDir, endpoint string) adapter.Adapter
	Version  string
	Adapters Registry
	Dynamic  func(string) adapter.Adapter
	Remote   func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) (int, error)
	Legacy   func(context.Context, []string, io.Reader, io.Writer, io.Writer) int
	Hook     func(io.Writer, string, string) error
}

var identifier = regexp.MustCompile("^[a-z0-9][a-z0-9._-]{0,31}$")

func (a App) registry(o options) (Registry, error) {
	r := Registry{"file": file.Adapter{OutputPath: o.values["out"]}}
	if a.OpenClaw != nil {
		r["openclaw"] = a.OpenClaw(o.values["profile-dir"], o.values["endpoint"])
	}
	for k, v := range a.Adapters {
		r[k] = v
	}
	for _, def := range o.adapters {
		id, path, ok := strings.Cut(def, "=")
		if !ok || !identifier.MatchString(id) || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("--adapter requires ID=/absolute/executable")
		}
		if _, exists := r[id]; exists {
			return nil, fmt.Errorf("adapter %q is already registered", id)
		}
		r[id] = adapter.External{Path: path}
	}
	return r, nil
}

func keys(r Registry) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a App) selectAdapter(r Registry, id string) (adapter.Adapter, error) {
	if x := r[id]; x != nil {
		return x, nil
	}
	if a.Dynamic != nil {
		if x := a.Dynamic(id); x != nil {
			return x, nil
		}
	}
	return nil, adapter.Fail("unavailable", "never", fmt.Sprintf("unknown adapter %q; use beam doctor", id))
}

func call[T any](ctx context.Context, a adapter.Adapter, r adapter.Request) (T, error) {
	var zero T
	v, err := a.Call(ctx, r)
	if err != nil {
		return zero, err
	}
	if x, ok := v.(T); ok {
		return x, nil
	}
	if p, ok := v.(*T); ok && p != nil {
		return *p, nil
	}
	return zero, fmt.Errorf("adapter returned an invalid %s result", r.Op)
}

func caps(ctx context.Context, a adapter.Adapter) (adapter.Capabilities, error) {
	c, err := call[adapter.Capabilities](ctx, a, adapter.Request{Op: "capabilities"})
	if err != nil {
		return c, err
	}
	if c.Protocol != adapter.Protocol || c.Adapter == "" || c.Version == "" {
		return c, adapter.Fail("unsupported_version", "never", "invalid adapter capability receipt")
	}
	return c, nil
}
