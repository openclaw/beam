package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The child represents an independently shipped harness. It reads the public
// JSON contract directly so an accidental dependency on Go SDK types fails here.
func TestMain(m *testing.M) {
	if os.Getenv("BEAM_E2E_EXTERNAL_CHILD") == "1" {
		if err := externalRequest(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func externalRequest() error {
	var input struct {
		Protocol int
		Request  struct {
			Op, Selection, Handle, RequestID, Instruction string
			Packet                                        json.RawMessage
		}
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.Protocol != 1 {
		return fmt.Errorf("unsupported protocol")
	}
	r := input.Request
	const handle = "local-reference"
	staged := map[string]string{"state": "staged", "handle": handle, "nextAction": "Send a fresh instruction"}
	var result any
	switch r.Op {
	case "capabilities":
		result = map[string]any{"protocol": 1, "adapter": "external", "version": "1.0.0", "source": []string{"list", "read"}, "destination": []string{"stageReference", "resolve", "send"}, "recovery": "manual"}
	case "list":
		result = map[string]any{"entries": []map[string]string{{"selection": "branch-a", "title": "Synthetic external session"}}}
	case "read":
		if r.Selection != "branch-a" {
			return fmt.Errorf("unexpected source selection")
		}
		result = map[string]any{
			"source": "external", "origin": map[string]string{"adapter": "external", "sourceHome": "temporary", "nativeSession": "branch-a"},
			"title": "Synthetic external session", "updatedAt": "2026-01-02T03:04:05Z", "completed": false, "completeness": "complete",
			"items": []map[string]string{{"type": "userMessage", "text": sourceMarker}, {"type": "agentMessage", "text": emailMarker}},
		}
	case "stageReference":
		if err := os.WriteFile("staged.json", r.Packet, 0600); err != nil {
			return err
		}
		result = staged
	case "resolve", "send":
		if r.Handle != handle {
			return fmt.Errorf("unknown local reference")
		}
		if _, err := os.Stat("staged.json"); err != nil {
			return err
		}
		result = staged
		if r.Op == "send" {
			if err := os.WriteFile("explicit-instruction", []byte(r.Instruction), 0600); err != nil {
				return err
			}
			result = map[string]string{"state": "accepted", "handle": handle, "requestId": r.RequestID}
		}
	default:
		return fmt.Errorf("unexpected operation %q", r.Op)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"protocol": 1, "ok": true, "result": result})
}

func TestIndependentAdapterThroughCLI(t *testing.T) {
	c := newCLI(t)
	child, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "BEAM_E2E_EXTERNAL_CHILD=1")
	invoke := func(args ...string) []byte {
		t.Helper()
		return c.run(t, nil, append(args, "--adapter", "external="+child)...)
	}
	// Use an independent expected projection. A wrong read, dropped item, or
	// uncleaned address must fail even when the executable advertises read.
	fixture := filepath.Join(c.dir, "fixture.json")
	const prefix = `{"version":2,"beamId":"0123456789abcdef0123456789abcdef","source":"external","title":"Fixture","updatedAt":"2026-01-02T03:04:05Z","completed":false,"items":[{"type":"userMessage","text":"`
	want := prefix + sourceMarker + `"},{"type":"agentMessage","text":"[REDACTED_EMAIL]"}]}`
	if err := os.WriteFile(fixture, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	var evidence struct{ Observed []string }
	machineResult(t, invoke("adapter", "test", "--in", "external", "--fixture", fixture, "--session", "branch-a", "--json"), &evidence)
	if !slices.Equal(evidence.Observed, []string{"capabilities", "selected-items-match"}) {
		t.Fatal(evidence)
	}
	if _, err := os.Stat(filepath.Join(c.dir, "staged.json")); !os.IsNotExist(err) {
		t.Fatal("source fixture unexpectedly staged")
	}
	var list struct{ Entries []struct{ Selection string } }
	machineResult(t, invoke("list", "--from", "external", "--json"), &list)
	if len(list.Entries) != 1 {
		t.Fatalf("listing returned %d entries", len(list.Entries))
	}
	snapshot := filepath.Join(c.dir, "reviewed.json")
	invoke("pack", "--from", "external", "--session", list.Entries[0].Selection, "--out", snapshot)
	var open struct{ State, Handle string }
	machineResult(t, invoke("open", snapshot, "--in", "external", "--json"), &open)
	if open.State != "staged" || open.Handle == "" {
		t.Fatalf("unexpected passive receipt: %+v", open)
	}
	staged, err := os.ReadFile(filepath.Join(c.dir, "staged.json"))
	if err != nil {
		t.Fatal(err)
	}
	requireReference(t, staged)
	if bytes.Contains(staged, []byte(emailMarker)) {
		t.Fatal("adapter received an unreviewed contact address")
	}
	var reopened struct{ State, Handle string }
	machineResult(t, invoke("resolve", "--in", "external", "--handle", open.Handle, "--json"), &reopened)
	if reopened != open {
		t.Fatalf("reopened a different reference: %+v", reopened)
	}
	instructionPath := filepath.Join(c.dir, "explicit-instruction")
	if _, err := os.Stat(instructionPath); !os.IsNotExist(err) {
		t.Fatalf("a passive operation sent an instruction: %v", err)
	}
	const text = "Use the synthetic reference"
	const id = "24210877-d3ec-4812-b83a-3e468122fcc7"
	var sent struct{ State, Handle, RequestID string }
	machineResult(t, invoke("send", "--in", "external", "--handle", open.Handle, "--message", text, "--request-id", id, "--json"), &sent)
	got, err := os.ReadFile(instructionPath)
	if err != nil || string(got) != text || sent.State != "accepted" || sent.Handle != open.Handle || sent.RequestID != id {
		t.Fatalf("explicit send receipt %+v, instruction %q, error %v", sent, got, err)
	}
}
