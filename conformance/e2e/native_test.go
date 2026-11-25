//go:build beam_native

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type receipt struct{ State, Handle, NativeSession string }

func (c cli) reopenCopy(t *testing.T, adapter string, packet []byte) receipt {
	t.Helper()
	var created, reopened receipt
	machineResult(t, c.run(t, packet, "open", "-", "--in", adapter, "--json"), &created)
	if created.State != "ready" || created.NativeSession == "" || created.Handle == "" {
		t.Fatalf("%s passive receipt: %+v", adapter, created)
	}
	machineResult(t, c.run(t, nil, "resolve", "--in", adapter, "--handle", created.Handle, "--json"), &reopened)
	if reopened.State != created.State || reopened.NativeSession != created.NativeSession {
		t.Fatalf("%s reopened a different native target: %+v", adapter, reopened)
	}
	return created
}

func nativeFixture(t *testing.T) (cli, *modelCalls) {
	t.Helper()
	c := newCLI(t)
	var paths []string
	for _, name := range []string{"codex", "opencode"} {
		path := os.Getenv("BEAM_E2E_" + strings.ToUpper(name))
		if !filepath.IsAbs(path) || !strings.EqualFold(strings.TrimSuffix(filepath.Base(path), ".exe"), name) {
			t.Fatalf("set BEAM_E2E_%s to an absolute native %s executable", strings.ToUpper(name), name)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("cannot use native %s executable: %v", name, err)
		}
		paths = append(paths, filepath.Dir(path))
	}
	c.env[0] = "PATH=" + strings.Join(append(paths, os.Getenv("PATH")), string(os.PathListSeparator))
	model := new(modelCalls)
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)
	home := filepath.Join(filepath.Dir(c.dir), "home")
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("model_provider = \"fixture\"\nmodel = \"fixture\"\n[model_providers.fixture]\nname = \"fixture\"\nbase_url = %q\nwire_api = \"responses\"\n", server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ocConfig, _ := json.Marshal(map[string]any{"model": "openai/gpt-4.1", "provider": map[string]any{"openai": map[string]any{"options": map[string]string{"baseURL": server.URL + "/v1", "apiKey": "fixture-only"}}}})
	c.env = append(c.env, "OPENCODE_CONFIG_CONTENT="+string(ocConfig), "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1")
	return c, model
}

// TestNativeRoundTrip requires installed real harness binaries and a synthetic
// loopback model. Each Beam launch and each native target reopen is a new process.
func TestNativeRoundTrip(t *testing.T) {
	c, model := nativeFixture(t)
	// Start with an OpenCode-native session, export it to Codex, then extract
	// that native Codex session to open a second, independent OpenCode copy.
	first := c.reopenCopy(t, "opencode", c.reviewed(t))
	fromOpen := c.run(t, nil, "pack", "--from", "opencode", "--session", first.NativeSession, "--out", "-")
	requireReference(t, fromOpen)
	codex := c.reopenCopy(t, "codex", fromOpen)
	fromCodex := c.run(t, nil, "pack", "--from", "codex", "--session", codex.NativeSession, "--out", "-")
	requireReference(t, fromCodex)
	second := c.reopenCopy(t, "opencode", fromCodex)
	if second.NativeSession == first.NativeSession {
		t.Fatal("an independent copy reopened the original OpenCode session")
	}
	if n := len(model.seen()); n != 0 {
		t.Fatalf("passive native copies and reads started %d model requests", n)
	}
	instruction := "Check only the synthetic CSV marker in the imported reference."
	req, _ := json.Marshal(map[string]any{"protocol": 1, "request": map[string]string{"op": "send", "handle": codex.Handle, "requestId": "00000000-0000-4000-8000-000000000001", "instruction": instruction}})
	var sent struct{ State string }
	machineResult(t, c.run(t, req, "adapter", "--in", "codex"), &sent)
	if sent.State != "completed" && sent.State != "accepted" {
		t.Fatalf("unexpected authorized send: %q", sent.State)
	}
	requests := model.seen()
	if len(requests) != 1 || !bytes.Contains(requests[0], []byte(sourceMarker)) || !bytes.Contains(requests[0], []byte(instruction)) {
		t.Fatalf("expected one authorized model turn with the reviewed reference and the new instruction; model calls=%d", len(requests))
	}
}

// The same adapter test command is available to third-party executables. Site
// evidence is emitted only after all checks and the loopback assertion pass.
func TestAdapterMatrix(t *testing.T) {
	c, model := nativeFixture(t)
	fixture, err := filepath.Abs("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	type evidence struct {
		TestVersion int    `json:"testVersion"`
		Adapter     string `json:"adapter"`
		Advertised  struct {
			Version     string   `json:"version"`
			Destination []string `json:"destination"`
		} `json:"advertised"`
		Observed []string `json:"observed"`
	}
	var rows []evidence
	for _, id := range []string{"codex", "opencode", "file"} {
		args := []string{"adapter", "test", "--in", id, "--fixture", fixture, "--json"}
		required := []string{"capabilities", "ready", "same-reference-resolved", "reopened-items-match"}
		if id == "file" {
			args = append(args, "--session", fixture, "--mode", "stage", "--out", "reference.md")
			required = []string{"capabilities", "selected-items-match", "manual"}
		} else {
			args = append(args, "--mode", "native")
		}
		var row evidence
		machineResult(t, c.run(t, nil, args...), &row)
		if row.TestVersion != 1 || row.Adapter != id || row.Advertised.Version == "" || !slices.Equal(row.Observed, required) {
			t.Fatalf("unexpected %s evidence: %+v", id, row)
		}
		rows = append(rows, row)
	}
	got, err := os.ReadFile(filepath.Join(c.dir, "reference.md"))
	if err != nil || !bytes.Equal(got, c.run(t, nil, "render", fixture)) {
		t.Fatalf("manual Markdown differs from reviewed fixture: %v", err)
	}
	if n := len(model.seen()); n != 0 {
		t.Fatalf("passive checks made %d model requests", n)
	}
	if path := os.Getenv("BEAM_E2E_MATRIX"); path != "" {
		report, err := json.MarshalIndent(map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "modelRequests": len(model.seen()), "adapters": rows}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(report, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// Use the installed OpenCode ACP server for a source turn, then read the record
// back through Beam. The other profile checks substitute a deterministic peer.
func TestACPXNativeOpenCode(t *testing.T) {
	c, model := nativeFixture(t)
	acpx := os.Getenv("BEAM_TEST_ACPX")
	if !filepath.IsAbs(acpx) {
		t.Fatal("set BEAM_TEST_ACPX to the installed acpx executable")
	}
	c.env[0] = "PATH=" + filepath.Dir(acpx) + string(os.PathListSeparator) + strings.TrimPrefix(c.env[0], "PATH=")
	config, err := json.Marshal(map[string]any{"agents": map[string]any{"opencode": map[string]any{"argv": []string{os.Getenv("BEAM_E2E_OPENCODE"), "acp"}}}, "ttl": 1, "defaultPermissions": "deny-all"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, ".acpxrc.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	client := c
	client.path = acpx
	var session struct {
		ID string `json:"acpxRecordId"`
	}
	if err := json.Unmarshal(client.run(t, nil, "--format", "json", "opencode", "sessions", "ensure", "--name", "csv"), &session); err != nil || session.ID == "" {
		t.Fatalf("create acpx:opencode record: %v", err)
	}
	defer client.run(t, nil, "--format", "quiet", "opencode", "sessions", "close", "csv")
	client.run(t, nil, "--format", "quiet", "opencode", "prompt", "-s", "csv", sourceMarker+" Contact "+emailMarker+".")
	before := len(model.seen())
	if before == 0 {
		t.Fatal("the explicit OpenCode turn did not reach the loopback provider")
	}
	var history struct {
		Source string
		Items  []struct{ Type, Text string }
	}
	var listed struct{ Entries []struct{ Selection string } }
	if err := json.Unmarshal(c.run(t, nil, "list", "--from", "acpx:opencode"), &listed); err != nil || len(listed.Entries) != 1 || listed.Entries[0].Selection != session.ID {
		t.Fatal("Beam did not list the selected native ACP record")
	}
	raw := c.run(t, nil, "pack", "--from", "acpx:opencode", "--session", session.ID)
	if err := json.Unmarshal(raw, &history); err != nil {
		t.Fatal(err)
	}
	foundUser, foundReply := false, false
	for _, item := range history.Items {
		foundUser = foundUser || item.Type == "userMessage" && strings.Contains(item.Text, sourceMarker+" Contact [REDACTED_EMAIL].")
		foundReply = foundReply || item.Type == "agentMessage" && strings.Contains(item.Text, "The fixture received your authorized prompt.")
	}
	if history.Source != "acpx" || !foundUser || !foundReply || len(model.seen()) != before {
		t.Fatalf("unexpected OpenCode conversation through acpx: %+v; calls before export=%d, after=%d", history, before, len(model.seen()))
	}
	if out := os.Getenv("BEAM_E2E_ACPX_PACKET"); out != "" {
		if err := os.WriteFile(out, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
