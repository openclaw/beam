package localhistory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable fixture")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	name := filepath.Join(dir, "gemini")
	for _, tc := range []struct {
		help      string
		supported bool
	}{{"old native options", false}, {"--session-file FILE", true}} {
		script := "#!/bin/sh\nif [ \"$1\" = --help ]; then echo '" + tc.help + "'; else echo 1.0.0; fi\n"
		if err := os.WriteFile(name, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		caps := Capabilities(context.Background(), "gemini")
		if caps.Version != "1.0.0" || slices.Contains(caps.Destination, "stageReference") != tc.supported {
			t.Fatal(caps)
		}
	}
}
