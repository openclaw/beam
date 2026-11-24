// Package e2e exercises the published CLI boundary through fresh processes.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const sourceMarker = "The synthetic CSV fixture keeps quoted newlines."
const emailMarker = "fixture-person@example.test"

type cli struct {
	path string
	dir  string
	env  []string
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Go's test subdirectories inherit the runner's umask. Native harnesses
	// check that the account owns every writable state ancestor.
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newCLI(t *testing.T) cli {
	t.Helper()
	path := os.Getenv("BEAM_E2E_BINARY")
	if path == "" {
		path = filepath.Join(t.TempDir(), "beam")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-o", path, "./cmd/beam")
		build.Dir = filepath.Join("..", "..")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build Beam: %v\n%s", err, output)
		}
	} else {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	root := privateTempDir(t)
	home, workspace := filepath.Join(root, "home"), filepath.Join(root, "workspace")
	for _, dir := range []string{home, workspace} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// These native tools can discover conversations and auth outside HOME through
	// platform-specific variables. Give the child an explicit, isolated environment.
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"CODEX_HOME=" + filepath.Join(home, ".codex"),
		"LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"),
		"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
	}
	for _, key := range []string{"SystemRoot", "COMSPEC", "PATHEXT", "TMP", "TEMP", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return cli{path: path, dir: workspace, env: env}
}

func (c cli) command(t *testing.T, input []byte, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir, cmd.Env, cmd.Stdin = c.dir, c.env, bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("Beam %s exceeded deadline: %v", args[0], ctx.Err())
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func (c cli) run(t *testing.T, input []byte, args ...string) []byte {
	t.Helper()
	stdout, stderr, err := c.command(t, input, args...)
	if err != nil {
		t.Fatalf("Beam %s: %v\n%s\n%s", args[0], err, stdout, stderr)
	}
	return stdout
}

func (c cli) reviewed(t *testing.T) []byte {
	t.Helper()
	// This is the host-to-cleaner contract. It uses no fixture-only Beam API.
	candidate := map[string]any{
		"source": "example", "origin": map[string]string{"adapter": "example", "sourceHome": "synthetic", "nativeSession": "csv"},
		"title": "Synthetic CSV review", "updatedAt": "2026-01-02T03:04:05Z", "completed": false, "completeness": "complete",
		"items": []map[string]string{
			{"type": "userMessage", "text": sourceMarker, "speakerId": "synthetic-user"},
			{"type": "agentMessage", "text": "Contact " + emailMarker + " for the synthetic test."},
		},
	}
	input, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, err := c.command(t, input, "clean", "--protocol", "1")
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, stderr)
	}
	if bytes.Contains(out, []byte(emailMarker)) || bytes.Contains(stderr, []byte(emailMarker)) {
		t.Fatal("clean exposed a synthetic contact address")
	}
	if !json.Valid(out) {
		t.Fatalf("clean stdout was not a single JSON value: %q", out)
	}
	var packet struct {
		Version int
		BeamID  string
		Items   []struct{ Type, Text, Speaker string }
	}
	if err := json.Unmarshal(out, &packet); err != nil {
		t.Fatal(err)
	}
	if packet.Version != 2 || len(packet.BeamID) != 32 || len(packet.Items) != 2 || packet.Items[0].Text != sourceMarker || packet.Items[0].Speaker != "p1" {
		t.Fatalf("unexpected cleaned packet: %s", out)
	}
	return out
}

func TestReviewAndRender(t *testing.T) {
	c := newCLI(t)
	packet := c.reviewed(t)
	path := filepath.Join(c.dir, "reviewed.beam.json")
	if err := os.WriteFile(path, packet, 0600); err != nil {
		t.Fatal(err)
	}
	rendered := c.run(t, nil, "render", path)
	if !bytes.Contains(rendered, []byte(sourceMarker)) || bytes.Contains(rendered, []byte(emailMarker)) {
		t.Fatalf("render lost the reference or leaked contact details: %s", rendered)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, packet) {
		t.Fatalf("render changed reviewed bytes: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(packet, &body); err != nil {
		t.Fatal(err)
	}
	body["owner"] = "synthetic"
	invalid, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(c.dir, "invalid.beam.json")
	if err := os.WriteFile(invalidPath, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.command(t, nil, "render", invalidPath); err == nil {
		t.Fatal("render accepted an undeclared ownership field")
	}
}

func requireReference(t *testing.T, data []byte) {
	t.Helper()
	var body struct{ Items []struct{ Text string } }
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Items {
		if strings.Contains(item.Text, sourceMarker) {
			return
		}
	}
	t.Fatal("re-export did not contain the reviewed reference")
}

func machineResult(t *testing.T, data []byte, out any) {
	t.Helper()
	var e struct {
		Protocol int
		OK       bool
		Result   json.RawMessage
	}
	if err := json.Unmarshal(data, &e); err != nil || !e.OK || e.Protocol != 1 {
		t.Fatalf("machine response: %s (%v)", data, err)
	}
	if err := json.Unmarshal(e.Result, out); err != nil {
		t.Fatal(err)
	}
}
