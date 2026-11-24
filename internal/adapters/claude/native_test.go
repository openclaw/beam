package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

// Exercise the actual CLI hook, including the packaged Beam executable called by Claude Code.
func TestNativeClaudeReference(t *testing.T) {
	if os.Getenv("BEAM_TEST_NATIVE") != "1" {
		t.Skip("set BEAM_TEST_NATIVE=1 with claude on PATH")
	}
	home := t.TempDir()
	project := t.TempDir()
	bin := os.Getenv("BEAM_E2E_BINARY")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "beam")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", bin, "../../../cmd/beam").CombinedOutput(); err != nil {
			t.Fatalf("build Beam: %v: %.500s", err, out)
		}
	}
	calls := make(chan []byte, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case calls <- body:
		default:
			t.Error("unexpected extra model activity")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fixture response"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		}
		for _, event := range events {
			var e struct{ Type string }
			if json.Unmarshal([]byte(event), &e) != nil {
				t.Error("invalid test data")
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, event)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "XDG_CONFIG_HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=" + os.Getenv("PATH"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "ANTHROPIC_API_KEY=synthetic-fixture-key", "ANTHROPIC_BASE_URL=" + server.URL}
	cmd := exec.CommandContext(ctx, bin, "open", "-", "--in", "claude", "--operation-id", "test", "--json")
	cmd.Dir = project
	cmd.Env = env
	data, err := packet.Marshal(packet.Packet{Version: 2, BeamID: strings.Repeat("a", 32), Source: "fixture", Title: "Fixture", UpdatedAt: "2026-01-01T00:00:00Z", Items: []packet.Item{{Type: "userMessage", Text: "SYNTHETIC_RAW_REFERENCE"}}})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Beam stage: %v: %.500s", err, out)
	}
	var res struct{ Result adapter.OpenResult }
	if json.Unmarshal(out, &res) != nil || res.Result.State != "staged" || len(calls) != 0 {
		t.Fatal("stage issued a request or returned an invalid receipt")
	}
	bridge := filepath.Join(filepath.Dir(res.Result.Artifact), "bridge")
	cmd = exec.CommandContext(ctx, "claude", "-p", "EXPLICIT_FIXTURE_QUESTION", "--plugin-dir", bridge, "--model", "claude-sonnet-4-6", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--max-turns", "1", "--output-format", "json")
	cmd.Dir = project
	cmd.Env = env
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("native Claude: %v: %.500s", err, out)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d model requests after explicit instruction", len(calls))
	}
	body := <-calls
	if !bytes.Contains(body, []byte("EXPLICIT_FIXTURE_QUESTION")) || !bytes.Contains(body, []byte("reference.beam.json")) || bytes.Contains(body, []byte("SYNTHETIC_RAW_REFERENCE")) {
		t.Fatal("native Claude model input did not preserve the reference boundary")
	}
	var reply struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &reply); err != nil || reply.SessionID == "" {
		t.Fatal("Claude returned no resumable session ID", err)
	}
	got, err := (Adapter{ConfigDir: filepath.Join(home, ".claude"), Project: project}).Call(ctx, adapter.Request{Op: "read", Selection: reply.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	items := got.(adapter.ReadResult).Items
	if len(items) != 2 || items[0].Text != "EXPLICIT_FIXTURE_QUESTION" || items[1].Text != "fixture response" {
		t.Fatal("selected Claude source did not contain exactly the two visible messages")
	}
}
