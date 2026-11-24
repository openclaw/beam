package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
func writeMainSession(t *testing.T, dir, fileID, sessionID, project string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"type": "user", "uuid": "first", "sessionId": sessionID, "cwd": project,
		"message": map[string]string{"role": "user", "content": "selected text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileID+".jsonl"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReadInOversizedInventory(t *testing.T) {
	a := Adapter{Project: t.TempDir(), ConfigDir: t.TempDir()}
	project, err := filepath.EvalSymlinks(a.Project)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "fixture")
	dir := filepath.Join(a.ConfigDir, "projects", "fixture")
	writeMainSession(t, dir, "chosen", "chosen", project)
	for i := range 10000 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".jsonl"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if _, err := a.Call(ctx, adapter.Request{Op: "list", Limit: 1}); err == nil || !strings.Contains(err.Error(), "inventory exceeds") {
		t.Fatalf("list limit: %v", err)
	}
	got, err := a.Call(ctx, adapter.Request{Op: "read", Selection: "chosen"})
	if err != nil || got.(adapter.ReadResult).Items[0].Text != "selected text" {
		t.Fatalf("read: %v, %v", got, err)
	}
	if _, err := a.Call(ctx, adapter.Request{Op: "read", Selection: "missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing selection: %v", err)
	}
}

func TestExactReadInCollidingProjectDirectories(t *testing.T) {
	a := Adapter{Project: t.TempDir(), ConfigDir: t.TempDir()}
	project, err := filepath.EvalSymlinks(a.Project)
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.Repeat("p", 200)
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", prefix+"x")
	first := filepath.Join(a.ConfigDir, "projects", prefix+"-a")
	second := filepath.Join(a.ConfigDir, "projects", prefix+"-b")
	// Older transcripts can lack sessionId. The filename spelling still selects them.
	writeMainSession(t, first, "Case", "", project)
	writeMainSession(t, second, "Case", "Case", project+"-other")
	read := func(id string) error {
		_, err := a.Call(context.Background(), adapter.Request{Op: "read", Selection: id})
		return err
	}
	if err := read("Case"); err != nil {
		t.Fatal(err)
	}
	if err := read("case"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("accepted a different legacy filename: %v", err)
	}
	writeMainSession(t, second, "Case", "Case", project)
	if err := read("Case"); err == nil || !strings.Contains(err.Error(), "ambiguous session ID") {
		t.Fatalf("duplicate selection: %v", err)
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
