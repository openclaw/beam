package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
)

type sourceFunc func(string, map[string]any) (string, error)

func (f sourceFunc) SourceCall(_ context.Context, op string, params any) (json.RawMessage, error) {
	b, err := f(op, params.(map[string]any))
	return json.RawMessage(b), err
}
func TestHistoryChronologyAndProjection(t *testing.T) {
	calls := 0
	s := sourceFunc(func(op string, params map[string]any) (string, error) {
		calls++
		if op != "chat.history" || params["sessionKey"] != "agent:main:example" {
			return "", fmt.Errorf("wrong scope")
		}
		if calls == 1 {
			return `{"sessionId":"s","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private thought"},{"type":"text","text":"visible answer"}],"timestamp":2000}],"hasMore":true,"olderCursor":"older","totalMessages":4}`, nil
		}
		if params["cursor"] != "older" {
			t.Fatal("wrong cursor")
		}
		return `{"sessionId":"s","messages":[{"role":"system","content":"private instructions"},{"role":"user","content":"visible question","timestamp":1000},{"role":"toolResult","content":"private result"}],"hasMore":false,"totalMessages":4}`, nil
	})
	page, err := read(context.Background(), s, "profile", adapter.Request{Op: "read", Selection: "agent:main:example", Limit: 50})
	if err != nil || calls != 2 || len(page.Items) != 2 || page.Items[0].Text != "visible question" || page.Items[1].Text != "visible answer" {
		t.Fatal(page, err)
	}
	b, _ := json.Marshal(page.Candidate)
	if strings.Contains(string(b), "private") {
		t.Fatal(string(b))
	}
}
func TestCatalogCarriesWholeLocator(t *testing.T) {
	loc := `{"catalogId":"c","hostId":"h","threadId":"t","agentId":"a","sourceHomeId":"home"}`
	s := sourceFunc(func(op string, p map[string]any) (string, error) {
		for k, v := range map[string]string{"catalogId": "c", "hostId": "h", "threadId": "t", "agentId": "a", "sourceHomeId": "home"} {
			if p[k] != v {
				t.Fatal("lost", k)
			}
		}
		if op != "sessions.catalog.read" {
			t.Fatal(op)
		}
		if p["cursor"] == nil {
			return `{"hostId":"h","threadId":"t","items":[{"type":"agentMessage","text":"newest"},{"type":"reasoning","text":"secret"}],"nextCursor":"older"}`, nil
		}
		return `{"hostId":"h","threadId":"t","items":[{"type":"toolResult","text":"secret"},{"type":"userMessage","text":"oldest","sender":{"identity":{"type":"profile","id":"user1"}}}]}`, nil
	})
	page, err := read(context.Background(), s, "profile", adapter.Request{Selection: loc, Limit: 50})
	if err != nil || len(page.Items) != 2 || page.Items[0].Text != "oldest" || page.Items[0].SpeakerID == "" || page.Items[1].Text != "newest" {
		t.Fatal(page, err)
	}
}
func TestNativeGenerationChange(t *testing.T) {
	s := sourceFunc(func(_ string, p map[string]any) (string, error) {
		if p["offset"] != nil {
			return `{"sessionId":"new","messages":[]}`, nil
		}
		return `{"sessionId":"old","messages":[],"hasMore":true,"nextOffset":3}`, nil
	})
	_, err := read(context.Background(), s, "profile", adapter.Request{Selection: "x", Limit: 50})
	if err == nil || adapter.Describe(err).Code != "source_changed" {
		t.Fatal(err)
	}
}
