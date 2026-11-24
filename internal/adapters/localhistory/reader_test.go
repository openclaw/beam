package localhistory

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
)

func TestReadPagesBoundEncodedWireBytes(t *testing.T) {
	text := strings.Repeat("\n", 1100000)
	r := Reader{Name: "pi", SourceHome: "fixture", Files: func(context.Context, string) ([]File, error) { return []File{{ID: "selected"}}, nil }, Load: func(context.Context, File, string) (History, error) {
		return History{Items: []Item{{Type: "userMessage", Text: text}, {Type: "agentMessage", Text: text}}}, nil
	}}
	q := adapter.Request{Op: "read", Selection: "selected", Limit: 200}
	for i, role := range []string{"userMessage", "agentMessage"} {
		got, err := r.Call(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		page := got.(adapter.ReadResult)
		envelope, err := adapter.Success(page)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > adapter.MaxResponseBytes || len(page.Items) != 1 || page.Items[0].Text != text || page.Items[0].Type != role {
			t.Fatal("page was oversized or split a native message")
		}
		if (page.NextCursor == "") != (i == 1) {
			t.Fatal("incorrect page boundary")
		}
		q.Cursor = page.NextCursor
	}
}

func TestVersionRequiresNativeReceivingFlag(t *testing.T) {
	caps := adapter.Capabilities{Version: "unavailable"}
	for _, tc := range []struct {
		help      string
		supported bool
	}{{"old native options", false}, {"--session-file FILE", true}} {
		result, err := qualify(caps, "--session-file", probeResult{text: "1.0.0"}, probeResult{text: tc.help})
		if err != nil || result.Version != "1.0.0" || slices.Contains(result.Destination, "stageReference") != tc.supported {
			t.Fatal(result, err)
		}
	}
	timeout := adapter.Fail("timeout", "same_request", "timed out")
	_, err := qualify(caps, "--session-file", probeResult{text: "1.0.0"}, probeResult{err: timeout})
	if !errors.Is(err, timeout) {
		t.Fatalf("collapsed installed executable probe error: %v", err)
	}
}
