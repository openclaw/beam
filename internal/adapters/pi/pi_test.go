package pi

import (
	"context"
	"encoding/json"
	"github.com/openclaw/beam/internal/adapter"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectedPiAncestry(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "--"+strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(filepath.ToSlash(project), "/"))+"--")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "native-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.Encode(map[string]any{"type": "session", "id": "selected", "version": 3, "cwd": project})
	for _, node := range []struct{ id, p, r, text string }{{"a", "", "user", "question"}, {"b", "a", "assistant", "alternate"}, {"c", "a", "toolResult", "private result"}, {"d", "c", "assistant", "active branch"}} {
		if err := enc.Encode(map[string]any{"type": "message", "id": node.id, "parentId": node.p, "message": map[string]any{"role": node.r, "content": []any{map[string]any{"type": "thinking", "text": "private"}, map[string]any{"type": "text", "text": node.text}}}}); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	a := Adapter{Project: project, AgentDir: root}
	for _, tc := range []struct{ selector, want string }{{"selected", "active branch"}, {"selected#b", "alternate"}} {
		got, err := a.Call(context.Background(), adapter.Request{Op: "read", Selection: tc.selector})
		if err != nil {
			t.Fatal(err)
		}
		items := got.(adapter.ReadResult).Items
		if len(items) != 2 || items[0].Text != "question" || items[1].Text != tc.want {
			t.Fatal(items)
		}
	}
}
