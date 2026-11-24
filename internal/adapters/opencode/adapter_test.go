package opencode

import (
	"context"
	"encoding/json"
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
		t.Skip("requires OpenCode v1.19.6 on PATH")
	}
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("OPENCODE_DISABLE_MODELS_FETCH", "true")
	t.Setenv("OPENCODE_DB", filepath.Join(home, "test.sqlite"))
	var modelRequests atomic.Int32
	var contextDelivered atomic.Bool
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		modelRequests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "alpha") && strings.Contains(string(body), "fresh synthetic instruction") {
			contextDelivered.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"chatcmpl_fixture","created":1,"model":"beam-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"synthetic reply"},"finish_reason":null}]}

data: {"id":"chatcmpl_fixture","created":1,"model":"beam-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`)
	}))
	defer mock.Close()
	conf, _ := json.Marshal(obj{"model": "fixture/beam-test", "small_model": "fixture/beam-test", "enabled_providers": []string{"fixture"}, "provider": obj{"fixture": obj{"npm": "@ai-sdk/openai-compatible", "name": "Fixture", "options": obj{"baseURL": mock.URL + "/v1", "apiKey": "synthetic-fixture"}, "models": obj{"beam-test": obj{"name": "synthetic"}}}}})
	t.Setenv("OPENCODE_CONFIG_CONTENT", string(conf))
	dest := Adapter{}
	p := packet.Packet{Title: "Synthetic transfer", Items: []packet.Item{{Type: "userMessage", Text: "alpha", Speaker: "p1"}, {Type: "agentMessage", Text: "beta"}}}
	v, err := dest.Call(context.Background(), adapter.Request{Op: "openCopy", OperationID: "fixture", Packet: p})
	if err != nil {
		t.Fatal(err)
	}
	got := v.(adapter.OpenResult)
	if got.State != "ready" {
		t.Fatalf("open: %+v", got)
	}
	v, err = dest.Call(context.Background(), adapter.Request{Op: "read", Selection: got.Handle})
	if err != nil {
		t.Fatal(err)
	}
	items := v.(adapter.ReadResult).Items
	if len(items) != 2 || items[0].Text != "alpha" || items[0].SpeakerID != "p1" || items[1].Text != "beta" {
		t.Fatalf("wrong synthetic items: %+v", items)
	}
	v, err = dest.Call(context.Background(), adapter.Request{Op: "list"})
	if err != nil {
		t.Fatal(err)
	}
	entries := v.(adapter.ListResult).Entries
	if len(entries) != 1 || entries[0].Selection != got.Handle {
		t.Fatalf("list failed: %+v", entries)
	}
	if modelRequests.Load() != 0 {
		t.Fatalf("passive copy sent %d model requests", modelRequests.Load())
	}
	v, err = dest.Call(context.Background(), adapter.Request{Op: "send", Handle: got.Handle, RequestID: "test-request", Instruction: "fresh synthetic instruction"})
	if err != nil {
		t.Fatal(err)
	}
	if v.(adapter.SendResult).State != "completed" || !contextDelivered.Load() {
		t.Fatalf("explicit user turn did not deliver the reference and instruction, requests=%d", modelRequests.Load())
	}
	v, err = dest.Call(context.Background(), adapter.Request{Op: "read", Selection: got.Handle})
	if err != nil {
		t.Fatal(err)
	}
	reopened := v.(adapter.ReadResult)
	if len(reopened.Items) < 4 || reopened.Items[len(reopened.Items)-1].Text != "synthetic reply" {
		t.Fatalf("new turn did not persist: %+v", reopened)
	}

}
