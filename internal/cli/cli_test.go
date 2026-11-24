package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

func invoke(t *testing.T, a App, input string, args ...string) (int, []byte, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := a.Run(context.Background(), args, strings.NewReader(input), &out, &stderr)
	return code, out.Bytes(), stderr.String()
}

func TestReviewedFileWorkflow(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(src, []byte("Owner private@example.org needs a newline regression test."), 0600); err != nil {
		t.Fatal(err)
	}
	a := App{Version: "test"}
	code, b, errout := invoke(t, a, "", "pack", "--from", "file", "--session", src)
	if code != 0 || bytes.Contains(b, []byte("private@example.org")) {
		t.Fatal(code, errout, string(b))
	}
	p, err := packet.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	code, exact, digest := invoke(t, a, string(b), "inspect", "-")
	if code != 0 || !bytes.Equal(exact, b) || !strings.Contains(digest, packet.Digest(b)) {
		t.Fatal(code, digest)
	}
	code, dry, _ := invoke(t, a, "", "push", "--from", "file", "--session", src, "--to", "file", "--dry-run")
	if code != 0 || !bytes.Equal(b, dry) {
		t.Fatal(code, string(dry))
	}
	dst := filepath.Join(dir, "history.md")
	code, _, _ = invoke(t, a, string(b), "open", "-", "--in", "file", "--out", dst, "--expect-digest", "wrong", "--json")
	if code != 5 {
		t.Fatalf("unexpected mismatch exit %d", code)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("destination touched on mismatching digest")
	}
	code, out, errout := invoke(t, a, string(b), "open", "-", "--in", "file", "--out", dst, "--expect-digest", packet.Digest(b), "--json")
	if code != 0 {
		t.Fatal(code, errout, string(out))
	}
	var env struct {
		OK     bool
		Result adapter.OpenResult
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !env.OK || env.Result.State != "manual" || string(got) != packet.Render(p) {
		t.Fatal(string(out), err)
	}
}
func TestCleanReportsOnlyCounts(t *testing.T) {
	candidate := clean.Candidate{Source: "test", Origin: clean.Origin{Adapter: "test", SourceHome: "home", NativeSession: "s"}, Title: "Example", UpdatedAt: "2026-05-11T00:00:00Z", Completeness: "complete", Items: []clean.Item{{Type: "userMessage", Text: "Remove privateword now"}}}
	b, _ := json.Marshal(candidate)
	code, out, diag := invoke(t, App{}, string(b), "clean", "--protocol", "1", "--redact", "privateword")
	if code != 0 || !json.Valid(out) || strings.Contains(string(out)+diag, "privateword") || !strings.Contains(diag, "redactions") {
		t.Fatal(code, string(out), diag)
	}
}
func TestCLIExternalProbeAndMissingCapabilities(t *testing.T) {
	code, b, _ := invoke(t, App{}, "", "open", "-", "--in", "file", "--unknown", "--json")
	var env adapter.Envelope
	if err := json.Unmarshal(b, &env); err != nil || env.Error == nil || code != 2 {
		t.Fatal(code, string(b), err)
	}
	code, b, _ = invoke(t, App{}, "", "capabilities", "--json")
	if code != 0 || !bytes.Contains(b, []byte("packetVersions")) {
		t.Fatal(code, string(b))
	}
}
