package codex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

func TestNativePassiveCopy(t *testing.T) {
	if os.Getenv("BEAM_TEST_NATIVE") != "1" {
		t.Skip("requires the qualified Codex binary on PATH")
	}
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var calls atomic.Int32
	var contextDelivered atomic.Bool
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			calls.Add(1)
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "quoted second line") && strings.Contains(string(body), "fresh synthetic instruction") {
				contextDelivered.Store(true)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"type":"response.created","response":{"id":"resp_fixture"}}

data: {"type":"response.output_item.done","item":{"type":"message","id":"msg_fixture","role":"assistant","content":[{"type":"output_text","text":"synthetic reply"}]}}

data: {"type":"response.completed","response":{"id":"resp_fixture","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}

`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	defer mock.Close()
	config := fmt.Sprintf("model = \"fixture\"\nmodel_provider = \"fixture\"\n[model_providers.fixture]\nname = \"fixture\"\nbase_url = %q\nwire_api = \"responses\"\n", mock.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	adapterImpl := Adapter{}
	p := packet.Packet{Title: "Synthetic copy", Items: []packet.Item{{Type: "userMessage", Text: "alpha\nquoted second line", Speaker: "p1"}, {Type: "agentMessage", Text: "beta"}}}
	res, err := adapterImpl.Call(context.Background(), adapter.Request{Op: "openCopy", OperationID: "fixture", Packet: p})
	if err != nil {
		t.Fatal(err)
	}
	opened := res.(adapter.OpenResult)
	if opened.State != "ready" {
		t.Fatalf("open: %+v", opened)
	}
	res, err = adapterImpl.Call(context.Background(), adapter.Request{Op: "read", Selection: opened.Handle, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := res.(adapter.ReadResult)
	if len(first.Items) != 1 || first.Items[0].Text != p.Items[0].Text || first.NextCursor == "" {
		t.Fatalf("bad synthetic page 1: %+v", first)
	}
	res, err = adapterImpl.Call(context.Background(), adapter.Request{Op: "read", Selection: opened.Handle, Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	second := res.(adapter.ReadResult)
	if len(second.Items) != 1 || second.Items[0].Text != "beta" || second.NextCursor != "" {
		t.Fatalf("bad synthetic page 2: %+v", second)
	}
	if calls.Load() != 0 {
		t.Fatalf("passive copy sent %d model requests", calls.Load())
	}
	res, err = adapterImpl.Call(context.Background(), adapter.Request{Op: "send", Handle: opened.Handle, RequestID: "test-request", Instruction: "fresh synthetic instruction"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(adapter.SendResult).State != "completed" || calls.Load() == 0 || !contextDelivered.Load() {
		t.Fatalf("explicit turn did not include restored history and fresh instruction: %+v / %d / %v", res, calls.Load(), contextDelivered.Load())
	}
	res, err = adapterImpl.Call(context.Background(), adapter.Request{Op: "read", Selection: opened.Handle})
	if err != nil {
		t.Fatal(err)
	}
	reopened := res.(adapter.ReadResult)
	if reopened.Title != p.Title || len(reopened.Items) < 4 || reopened.Items[len(reopened.Items)-1].Text != "synthetic reply" {
		t.Fatalf("new turn failed to persist: %+v", reopened)
	}

}
