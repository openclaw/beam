package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	native "github.com/openclaw/beam/integrations/gemini"
	"github.com/openclaw/beam/internal/adapter"
)

// Gemini writes the imported session before its non-interactive credential check.
func TestNativeGeminiSessionFile(t *testing.T) {
	if os.Getenv("BEAM_TEST_NATIVE") != "1" {
		t.Skip("set BEAM_TEST_NATIVE=1 with gemini on PATH")
	}
	home, project, stage := t.TempDir(), t.TempDir(), t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Prepare(stage, stage, "fixture-source-id"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gemini", "--session-file", filepath.Join(stage, "reference.gemini.json"), "--list-sessions")
	cmd.Dir = project
	cmd.Env = []string{"HOME=" + home, "GEMINI_CLI_HOME=" + home, "PATH=" + os.Getenv("PATH"), "GEMINI_CLI_NO_RELAUNCH=true", "CI=true", "NO_COLOR=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 41 || !strings.Contains(string(out), "Auth method") {
			t.Fatalf("native Gemini import: %v: %.600s", err, out)
		}
	}
	paths, err := filepath.Glob(filepath.Join(home, ".gemini", "tmp", "*", "chats", "session-*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("wanted one native import, found %d (%v)", len(paths), err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var meta metadata
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || json.Unmarshal([]byte(lines[0]), &meta) != nil || meta.SessionID == "" || meta.SessionID == "fixture-source-id" {
		t.Fatal("native import did not assign a fresh identity")
	}
	for index, expected := range []string{"info", "gemini"} {
		var msg message
		if json.Unmarshal([]byte(lines[index+1]), &msg) != nil || msg.Type != expected {
			t.Fatalf("wrong message type at %d", index)
		}
	}
	a := Adapter{Project: project, ConfigDir: filepath.Join(home, ".gemini")}
	got, err := a.Call(ctx, adapter.Request{Op: "read", Selection: meta.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	items := got.(adapter.ReadResult).Items
	if len(items) != 1 || items[0].Type != "agentMessage" || !strings.Contains(items[0].Text, "reference.beam.json") {
		t.Fatal("Beam could not read the neutral notice from the native import")
	}
}
