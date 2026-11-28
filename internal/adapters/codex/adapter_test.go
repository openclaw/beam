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
	"github.com/openclaw/beam/internal/adapters/native"
	"github.com/openclaw/beam/internal/clean"
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

func TestCopiedGoalBudget(t *testing.T) {
	budget := int64(100)
	for _, tc := range []struct {
		name      string
		source    *packet.Goal
		remaining int64
		want      bool
	}{
		{"none", nil, 0, false},
		{"unlimited", &packet.Goal{Objective: "repair", Status: "active"}, 0, true},
		{"remaining", &packet.Goal{Objective: "repair", Status: "active", TokenBudget: &budget, TokensUsed: 40}, 60, true},
		{"exhausted", &packet.Goal{Objective: "repair", Status: "budgetLimited", TokenBudget: &budget, TokensUsed: 100}, 0, false},
		{"overspent", &packet.Goal{Objective: "repair", Status: "budgetLimited", TokenBudget: &budget, TokensUsed: 120}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := copiedGoal(tc.source)
			if (got != nil) != tc.want {
				t.Fatalf("got %+v", got)
			}
			if got != nil && (got.Status != "paused" || got.TokensUsed != 0 || (got.TokenBudget != nil && *got.TokenBudget != tc.remaining)) {
				t.Fatalf("invalid native goal %+v", got)
			}
		})
	}
}

func TestNativeGoalRoundTrip(t *testing.T) {
	if os.Getenv("BEAM_TEST_NATIVE") != "1" {
		t.Skip("requires the qualified Codex binary on PATH")
	}
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "passive import must not call a model", http.StatusBadRequest)
	}))
	defer mock.Close()
	config := fmt.Sprintf("model = \"fixture\"\nmodel_provider = \"fixture\"\n[model_providers.fixture]\nname = \"fixture\"\nbase_url = %q\nwire_api = \"responses\"\n", mock.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := start(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	var source reply
	if err := c.call("thread/start", obj{"ephemeral": false}, &source); err != nil {
		t.Fatal(err)
	}
	if err := c.call("thread/inject_items", obj{"threadId": source.Thread.ID, "items": []obj{{"type": "message", "role": "user", "content": native.Frame(packet.Packet{Items: []packet.Item{{Type: "userMessage", Text: "repair the importer"}}})}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.call("thread/goal/set", obj{"threadId": source.Thread.ID, "objective": "/goal repair the importer", "status": "paused", "tokenBudget": 1000}, nil); err != nil {
		t.Fatal(err)
	}
	before, err := readGoal(c, source.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.close()
	a := Adapter{}
	readResult, err := a.Call(ctx, adapter.Request{Op: "read", Selection: source.Thread.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := clean.Build(readResult.(adapter.ReadResult).Candidate, clean.Options{})
	if err != nil || p.Goal == nil || p.Version != 3 {
		t.Fatalf("exported goal: %+v / %v", p.Goal, err)
	}
	opened, err := a.Call(ctx, adapter.Request{Op: "openCopy", OperationID: "goal-fixture", Packet: p})
	if err != nil {
		t.Fatal(err)
	}
	result := opened.(adapter.OpenResult)
	if result.State != "ready" || !strings.Contains(result.NextAction, "/goal resume") {
		t.Fatalf("copy: %+v", result)
	}
	probe, err := start(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.close()
	after, err := readGoal(probe, source.Thread.ID)
	if err != nil || after == nil || *after.TokenBudget != *before.TokenBudget || after.Objective != before.Objective || after.Status != before.Status || after.TokensUsed != before.TokensUsed {
		t.Fatalf("source changed: %+v / %v", after, err)
	}
	goal, err := readGoal(probe, result.Handle)
	if err != nil || goal == nil || goal.Status != "paused" || goal.Objective != before.Objective || *goal.TokenBudget != 1000 || goal.TokensUsed != 0 {
		t.Fatalf("destination goal: %+v / %v", goal, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("passive goal copy made %d model calls", calls.Load())
	}
	p.Goal.TokensUsed = *p.Goal.TokenBudget
	p.Goal.Status = "budgetLimited"
	exhausted, err := a.Call(ctx, adapter.Request{Op: "openCopy", OperationID: "exhausted-fixture", Packet: p})
	if err != nil {
		t.Fatal(err)
	}
	exhaustedResult := exhausted.(adapter.OpenResult)
	if exhaustedResult.State != "ready" || !strings.Contains(exhaustedResult.NextAction, "budget is exhausted") {
		t.Fatalf("exhausted copy: %+v", exhaustedResult)
	}
	if goal, err := readGoal(probe, exhaustedResult.Handle); err != nil || goal != nil {
		t.Fatalf("exhausted goal received a new allowance: %+v / %v", goal, err)
	}
	reexported, err := a.Call(ctx, adapter.Request{Op: "read", Selection: exhaustedResult.Handle})
	if err != nil {
		t.Fatal(err)
	}
	history := reexported.(adapter.ReadResult)
	if history.Goal != nil || !strings.Contains(history.Items[len(history.Items)-1].Text, "Historical goal (reference only):") {
		t.Fatal("exhausted goal was lost or recreated")
	}
	// A source with goals disabled still exports ordinary history and reports
	// unavailable goal metadata instead of treating it as an absent goal.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config+"\n[features]\ngoals = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	disabled, err := a.Call(ctx, adapter.Request{Op: "read", Selection: source.Thread.ID})
	if err != nil {
		t.Fatal(err)
	}
	partial := disabled.(adapter.ReadResult)
	if partial.Completeness != "partial" || partial.Goal != nil || !strings.Contains(partial.Items[len(partial.Items)-1].Text, "disabled goals") {
		t.Fatalf("disabled goal export: %+v", partial)
	}
	if calls.Load() != 0 {
		t.Fatalf("goal handling made %d model calls", calls.Load())
	}
}
