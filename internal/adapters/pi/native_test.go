package pi

import (
	"bufio"
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
	"strings"
	"testing"
	"time"

	native "github.com/openclaw/beam/integrations/pi"
)

// An installed Pi serves RPC and sends one explicit instruction to a loopback model.
func TestNativePiReference(t *testing.T) {
	if os.Getenv("BEAM_TEST_NATIVE") != "1" {
		t.Skip("set BEAM_TEST_NATIVE=1 with pi on PATH")
	}
	requests := make(chan string, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case requests <- string(body):
		default:
			t.Error("unexpected additional model requests")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"fixture\",\"model\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture answer\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	home := t.TempDir()
	project := t.TempDir()
	stage := t.TempDir()
	if err := native.Prepare(stage, stage, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "reference.beam.json"), []byte("SYNTHETIC_RAW_REFERENCE"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`{"providers":{"beam-fixture":{"api":"openai-completions","baseUrl":"%s/v1","apiKey":"fixture","models":[{"id":"fixture","name":"fixture"}]}}}`, model.URL)
	if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pi", "--offline", "--mode", "rpc", "--provider", "beam-fixture", "--model", "fixture", "--no-tools", "--no-extensions", "-e", filepath.Join(stage, "reference.mjs"))
	cmd.Dir = project
	cmd.Env = []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + home, "PI_OFFLINE=1", "PATH=" + os.Getenv("PATH")}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	cmd.Stderr = &errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); cancel(); _ = cmd.Wait() }()
	enc := json.NewEncoder(in)
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 1<<20)
	type event struct {
		Type, Command string
		Success       bool
		Data          struct{ Messages []json.RawMessage }
		Error         string
	}
	until := func(typ, command string) event {
		t.Helper()
		for scan.Scan() {
			var e event
			if json.Unmarshal(scan.Bytes(), &e) != nil {
				continue
			}
			if e.Type == typ && e.Command == command {
				if typ == "response" && !e.Success {
					t.Fatal(e.Error)
				}
				return e
			}
		}
		t.Fatalf("native Pi ended: %v", ctx.Err())
		return event{}
	}
	send := func(command map[string]string) {
		t.Helper()
		if err := enc.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]string{"type": "get_messages"})
	if len(until("response", "get_messages").Data.Messages) != 0 || len(requests) != 0 {
		t.Fatal("passive startup initiated a turn")
	}
	send(map[string]string{"type": "prompt", "message": "EXPLICIT_FIXTURE_QUESTION"})
	until("agent_end", "")
	var body string
	select {
	case body = <-requests:
	default:
		t.Fatal("Pi produced no loopback model request")
	}
	if !strings.Contains(body, "EXPLICIT_FIXTURE_QUESTION") || !strings.Contains(body, "reference.beam.json") || strings.Contains(body, "SYNTHETIC_RAW_REFERENCE") {
		t.Fatal("incorrect model-visible history or missing extension")
	}
	send(map[string]string{"type": "get_messages"})
	var notices int
	for _, raw := range until("response", "get_messages").Data.Messages {
		var m struct{ Role, CustomType string }
		json.Unmarshal(raw, &m)
		if m.Role == "custom" && m.CustomType == "beam-reference" {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("got %d Beam notices", notices)
	}
}
