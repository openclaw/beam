package acp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

func testPacket() packet.Packet {
	return packet.Packet{Version: 2, BeamID: "0123456789abcdef0123456789abcdef", Source: "file", Title: "Fixture", UpdatedAt: "2026-01-01T00:00:00Z", Completed: true, Items: []packet.Item{{Type: "agentMessage", Text: "Reviewed reference for fixture"}}}
}
func TestVisibleTurns(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"User":{"content":[{"Text":"selected"},{"Mention":{"content":"private attachment"}}]}}`), json.RawMessage(`{"Agent":{"content":[{"Thinking":{"text":"private thought"}},{"ToolUse":{"name":"private tool"}},{"Text":"visible"}],"tool_results":{"r":"private result"}}}`), json.RawMessage(`"Resume"`)}
	got, err := visibleTurns(raw)
	if err != nil || len(got) != 2 || got[0].Text != "selected" || got[1].Text != "visible" {
		t.Fatalf("visible projection = %v, %v", got, err)
	}
}
func TestPromptCompletedRequiresMatchingRequest(t *testing.T) {
	events := "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"session/prompt\",\"params\":{\"sessionId\":\"x\"}}\n"
	term := func(id, reason string) []byte {
		return []byte(events + "{\"jsonrpc\":\"2.0\",\"id\":" + id + ",\"result\":{\"stopReason\":\"" + reason + "\"}}\n")
	}
	if !promptCompleted(term("2", "end_turn"), "x") || promptCompleted(term("1", "end_turn"), "x") || promptCompleted(term("2", "end_turn"), "other") || promptCompleted(term("2", "cancelled"), "x") {
		t.Fatal("terminal correlation")
	}
}

func acpxFixture(t *testing.T) (Adapter, string, []string) {
	t.Helper()
	bin := os.Getenv("BEAM_TEST_ACPX")
	if bin == "" {
		t.Skip("set BEAM_TEST_ACPX to the installed acpx 0.22 CLI")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	// Short fixture homes also fit Unix-domain socket limits on macOS.
	root, err := os.MkdirTemp("", "beam-acp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	for _, p := range []string{home, project} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	peer, err := filepath.Abs("testdata/agent.mjs")
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"agents": map[string]any{"fixture": map[string]any{"argv": []string{node, peer}}}, "ttl": 1, "defaultPermissions": "deny-all"}
	data, _ := json.Marshal(config)
	if err = os.WriteFile(filepath.Join(project, ".acpxrc.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEAM_STATE", filepath.Join(root, "journal.sqlite"))
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	return Adapter{Agent: "fixture", Binary: bin, Cwd: project, StateRoot: filepath.Join(root, "refs"), env: env}, project, env
}

func boundSends(t *testing.T, a Adapter) bool {
	t.Helper()
	supported := slices.Contains(a.capabilities(t.Context()).Destination, "send")
	if os.Getenv("BEAM_TEST_ACPX_BOUND_SEND") == "1" && !supported {
		t.Fatal("the tested acpx should support exact-record sends")
	}
	return supported
}

func seedNativeConversation(t *testing.T, a Adapter, instruction string) string {
	t.Helper()
	raw, err := a.run(t.Context(), "", 65536, "--format", "json", "fixture", "sessions", "ensure", "--name", "native-seed")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID      string `json:"acpxRecordId"`
		Created bool
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" || !created.Created {
		t.Fatal("native seed creation")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = a.run(ctx, "", 8192, "--format", "quiet", "fixture", "sessions", "close", "native-seed")
	})
	if _, err := a.run(t.Context(), instruction, 65536, "--format", "quiet", "fixture", "prompt", "-s", "native-seed", "--file", "-"); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestReleasedACPXInterop(t *testing.T) {
	a, project, _ := acpxFixture(t)
	ctx := t.Context()
	call := func(r adapter.Request) any {
		t.Helper()
		v, e := a.Call(ctx, r)
		if e != nil {
			t.Fatalf("%s: %v", r.Op, e)
		}
		return v
	}
	stage := call(adapter.Request{Op: "stageReference", OperationID: "fixture-stage", Packet: testPacket()}).(adapter.OpenResult)
	if stage.State != "staged" {
		t.Fatal(stage)
	}
	lookup := call(adapter.Request{Op: "lookupOperation", OperationID: "fixture-stage"}).(adapter.OpenResult)
	if lookup.Handle != stage.Handle {
		t.Fatal(lookup)
	}
	if _, err := os.Stat(filepath.Join(project, "fixture-acp.ndjson")); !os.IsNotExist(err) {
		t.Fatalf("stage started an agent: %v", err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = a.run(c, "", 8192, "--format", "quiet", "fixture", "sessions", "close", "beam-"+stage.Handle)
	})
	send := adapter.Request{Op: "send", Handle: stage.Handle, RequestID: "first-send", Instruction: "fresh instruction"}
	if !boundSends(t, a) {
		_, err := a.Call(ctx, send)
		if err == nil || adapter.Describe(err).Code != "unsupported_operation" {
			t.Fatalf("unguarded acpx send: %v", err)
		}
		if _, err := os.Stat(filepath.Join(project, "fixture-acp.ndjson")); !os.IsNotExist(err) {
			t.Fatal("unsupported send started a peer")
		}
		id := seedNativeConversation(t, a, "explicit native prompt")
		read := call(adapter.Request{Op: "read", Selection: id}).(adapter.ReadResult)
		text, _ := json.Marshal(read.Items)
		if read.Completeness != "partial" || strings.Contains(string(text), "private-fixture") || !strings.Contains(string(text), "visible synthetic response") {
			t.Fatal(string(text))
		}
		return
	}
	sent := call(send).(adapter.SendResult)
	if sent.State != "completed" {
		t.Fatal(sent)
	}
	if call(send).(adapter.SendResult) != sent {
		t.Fatal("retry changed receipt")
	}
	resolved := call(adapter.Request{Op: "resolve", Handle: stage.Handle}).(adapter.OpenResult)
	if resolved.State != "ready" || resolved.NativeSession == "" {
		t.Fatal(resolved)
	}
	listed := call(adapter.Request{Op: "list"}).(adapter.ListResult)
	if len(listed.Entries) != 1 || listed.Entries[0].Selection != resolved.NativeSession {
		t.Fatal(listed)
	}
	got := call(adapter.Request{Op: "read", Selection: resolved.NativeSession}).(adapter.ReadResult)
	encoded, _ := json.Marshal(got.Items)
	if got.Completeness != "partial" || !strings.Contains(string(encoded), "fresh instruction") || !strings.Contains(string(encoded), "visible synthetic response") || strings.Contains(string(encoded), "private-fixture") {
		t.Fatalf("incorrect read projection: %s", encoded)
	}
	trace, err := os.ReadFile(filepath.Join(project, "fixture-acp.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(trace), `"method":"session/prompt"`) != 1 || !strings.Contains(string(trace), "Reviewed reference for fixture") {
		t.Fatal("expected one explicit seeded prompt")
	}
	send.Instruction = "different"
	_, err = a.Call(ctx, send)
	if err == nil || adapter.Describe(err).Code != "request_identity_conflict" {
		t.Fatalf("identity conflict: %v", err)
	}
	// Cancellation belongs to this request's foreground process. ACPX generates
	// the wire id, so an uncertain cancellation receipt must fence replay.
	send.RequestID = "cancel-me"
	send.Instruction = "fixture-wait-for-cancel"
	cancelCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, e := a.Call(cancelCtx, send); result <- e }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		log, _ := os.ReadFile(filepath.Join(project, "fixture-acp.ndjson"))
		if strings.Contains(string(log), send.Instruction) {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("waiting prompt did not reach fixture")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not settle")
	}
	if err == nil || adapter.Describe(err).Code != "needs_reconciliation" {
		t.Fatalf("cancel outcome: %v", err)
	}
	if _, err = a.Call(ctx, send); err == nil || adapter.Describe(err).Code != "needs_reconciliation" {
		t.Fatalf("ambiguous replay: %v", err)
	}
	log, _ := os.ReadFile(filepath.Join(project, "fixture-acp.ndjson"))
	if strings.Count(string(log), `"method":"session/prompt"`) != 2 {
		t.Fatal("cancelled prompt was replayed")
	}
	if !strings.Contains(string(log), `"method":"session/cancel"`) {
		t.Fatal("cancel was not delivered to the active prompt")
	}
	if resolved := call(adapter.Request{Op: "resolve", Handle: stage.Handle}).(adapter.OpenResult); resolved.State != "needs_reconciliation" {
		t.Fatal(resolved)
	}
}

func TestBeamBinaryACPX(t *testing.T) {
	a, project, env := acpxFixture(t)
	name := filepath.Join(t.TempDir(), "beam")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", name, "./cmd/beam")
	build.Dir = filepath.Join("..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build beam: %v %s", err, out)
	}
	path := filepath.Dir(a.Binary) + string(os.PathListSeparator) + os.Getenv("PATH")
	env = append(env[:2], "PATH="+path, "BEAM_STATE="+os.Getenv("BEAM_STATE"))
	invoke := func(input string, dst any, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), name, args...)
		cmd.Dir, cmd.Env, cmd.Stdin = project, env, strings.NewReader(input)
		out, err := cmd.Output()
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				t.Fatalf("%s: %v %s %s", args[0], err, out, e.Stderr)
			}
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, dst); err != nil {
			t.Fatalf("%s output: %v", args[0], err)
		}
	}
	contents, err := packet.Marshal(testPacket())
	if err != nil {
		t.Fatal(err)
	}
	var stage adapter.OpenResult
	invoke(string(contents), &stage, "open", "-", "--in", "acpx:fixture", "--operation-id", "binary-stage")
	if stage.State != "staged" || stage.Handle == "" {
		t.Fatal(stage)
	}
	var again adapter.OpenResult
	invoke("", &again, "lookup", "--in", "acpx:fixture", "--operation-id", "binary-stage")
	if again.Handle != stage.Handle {
		t.Fatal(again)
	}
	trace := filepath.Join(project, "fixture-acp.ndjson")
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("stage started a peer: %v", err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = a.run(c, "", 8192, "--format", "quiet", "fixture", "sessions", "close", "beam-"+stage.Handle)
	})
	var id string
	if boundSends(t, a) {
		var sent adapter.SendResult
		for range 2 {
			invoke("", &sent, "send", "--in", "acpx:fixture", "--handle", stage.Handle, "--request-id", "binary-first", "--message", "binary fresh instruction")
			if sent.State != "completed" {
				t.Fatal(sent)
			}
		}
		var bound adapter.OpenResult
		invoke("", &bound, "resolve", "--in", "acpx:fixture", "--handle", stage.Handle)
		if bound.State != "ready" {
			t.Fatal(bound)
		}
		id = bound.NativeSession
	} else {
		cmd := exec.CommandContext(t.Context(), name, "send", "--in", "acpx:fixture", "--handle", stage.Handle, "--request-id", "binary-first", "--message", "binary fresh instruction", "--json")
		cmd.Dir, cmd.Env = project, env
		out, err := cmd.Output()
		var reply adapter.Envelope
		if err == nil || json.Unmarshal(out, &reply) != nil || reply.Error == nil || reply.Error.Code != "unsupported_operation" {
			t.Fatal("unguarded binary send", err, string(out))
		}
		if _, err := os.Stat(trace); !os.IsNotExist(err) {
			t.Fatal("unsupported binary send started a peer")
		}
		id = seedNativeConversation(t, a, "binary fresh instruction")
	}
	var list adapter.ListResult
	invoke("", &list, "list", "--from", "acpx:fixture")
	if len(list.Entries) != 1 || list.Entries[0].Selection != id {
		t.Fatal(list)
	}
	before, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	var packed packet.Packet
	invoke("", &packed, "pack", "--from", "acp:fixture", "--session", id)
	after, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(packed)
	if string(before) != string(after) || strings.Count(string(after), `"method":"session/prompt"`) != 1 ||
		!strings.Contains(string(text), "binary fresh instruction") || !strings.Contains(string(text), "visible synthetic response") ||
		strings.Contains(string(text), "private-fixture") || !packed.Truncated {
		t.Fatalf("incorrect binary handoff: %s", text)
	}
}

// Exercise the interleaving between Beam's check and acpx's queue admission.
// Replacing the name is a native acpx operation; both queue owners use the real peer.
func TestACPXRefusesReplacedDestination(t *testing.T) {
	a, project, _ := acpxFixture(t)
	if !boundSends(t, a) {
		t.Skip("installed acpx supports passive references only")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("the interleaving fixture requires a POSIX shell")
	}
	wrapper := filepath.Join(project, "interleave-acpx")
	const script = `
name=
file=false
prompt=false
prev=
for arg in "$@"; do
  if [ "$prev" = "-s" ]; then name=$arg; fi
  case "$arg" in prompt) prompt=true ;; --file) file=true ;; esac
  prev=$arg
done
if [ "$prompt" = true ] && [ "$file" = true ] && [ -n "$name" ]; then
  "$REAL_ACPX" --format quiet fixture sessions new --name "$name" >/dev/null || exit 1
fi
exec "$REAL_ACPX" "$@"
`
	if err := os.WriteFile(wrapper, []byte("#!"+sh+"\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	a.env = append(a.env, "REAL_ACPX="+a.Binary)
	a.Binary = wrapper
	value, err := a.Call(t.Context(), adapter.Request{Op: "stageReference", OperationID: "swap", Packet: testPacket()})
	if err != nil {
		t.Fatal(err)
	}
	stage := value.(adapter.OpenResult)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = a.run(ctx, "", 8192, "--format", "quiet", "fixture", "sessions", "close", "beam-"+stage.Handle)
	})
	_, err = a.Call(t.Context(), adapter.Request{Op: "send", Handle: stage.Handle, RequestID: "swap-prompt", Instruction: "must never reach replacement"})
	if err == nil {
		t.Fatal("accepted a replaced destination")
	}
	raw, err := os.ReadFile(filepath.Join(project, "fixture-acp.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"method":"session/new"`) < 2 || strings.Contains(string(raw), `"method":"session/prompt"`) {
		t.Fatal("reassigning a session name delivered a prompt or failed to replace the destination")
	}
}
