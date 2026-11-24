package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	native "github.com/openclaw/beam/integrations/claude"
	"github.com/openclaw/beam/internal/adapter"
)

func TestSelectedMainBranch(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	config := t.TempDir()
	dir := filepath.Join(config, "projects", unsafeDirChars.ReplaceAllString(project, "-"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, "test-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(file)
	labels := []struct {
		id, parent, role string
		content          any
		side             bool
	}{
		{"first", "", "user", "original question", false}, {"abandoned", "first", "assistant", "abandoned answer", false},
		{"side", "first", "assistant", "private subagent output", true},
		{"kept", "first", "assistant", []any{map[string]any{"type": "thinking", "text": "private reasoning"}, map[string]any{"type": "text", "text": "selected answer"}}, false},
		{"tool", "kept", "user", []any{map[string]any{"type": "tool_result", "content": "secret result"}}, false},
	}
	for _, l := range labels {
		if err := enc.Encode(map[string]any{"type": l.role, "message": map[string]any{"role": l.role, "content": l.content}, "parentUuid": l.parent, "uuid": l.id, "isSidechain": l.side, "sessionId": "test-session", "cwd": project, "timestamp": "2026-11-23T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	a := Adapter{Project: project, ConfigDir: config}
	call := func(id string, limit int, cursor string) adapter.ReadResult {
		t.Helper()
		got, err := a.Call(context.Background(), adapter.Request{Op: "read", Selection: id, Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		return got.(adapter.ReadResult)
	}
	first := call("test-session", 1, "")
	if len(first.Items) != 1 || first.Items[0].Text != "original question" || first.NextCursor == "" {
		t.Fatal(first)
	}
	second := call("test-session", 1, first.NextCursor)
	if len(second.Items) != 1 || second.Items[0].Text != "selected answer" || second.NextCursor != "" {
		t.Fatal(second)
	}
	branch := call("test-session#abandoned", 50, "")
	if len(branch.Items) != 2 || branch.Items[1].Text != "abandoned answer" {
		t.Fatal(branch)
	}
	if _, err := a.Call(context.Background(), adapter.Request{Op: "read", Selection: "test-session#side"}); err == nil {
		t.Fatal("exposed sidechain")
	}
	if _, err := a.Call(context.Background(), adapter.Request{Op: "read", Selection: "../test-session"}); err == nil {
		t.Fatal("accepted path traversal")
	}
}
func TestNativeClaudePluginValidation(t *testing.T) {
	if os.Getenv("BEAM_TEST_NATIVE") != "1" {
		t.Skip("set BEAM_TEST_NATIVE=1 to run installed Claude Code")
	}
	exe, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := native.Prepare(dir, dir, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "plugin", "validate", filepath.Join(dir, "bridge"), "--json")
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=" + os.Getenv("PATH")}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native plugin validation: %v: %s", err, output)
	}
}
