package gemini

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/openclaw/beam/internal/adapter"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportKeepsOnlyVisibleText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.json")
	const body = `[{"role":"user","parts":[{"text":"<session_context>private workspace</session_context>"}]},{"role":"user","parts":[{"text":"fix parser"},{"functionResponse":{"response":"private output"}}]},{"role":"model","parts":[{"thought":true,"text":"private thought"},{"text":"all tested"}]}]`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := (Adapter{}).Call(context.Background(), adapter.Request{Op: "read", Selection: "export:" + path})
	if err != nil {
		t.Fatal(err)
	}
	result := got.(adapter.ReadResult)
	if result.Completeness != "partial" || len(result.Items) != 2 || result.Items[0].Text != "fix parser" || result.Items[1].Text != "all tested" {
		t.Fatal(result)
	}
}
func TestNativeProjectSelectionRewindsAndPatches(t *testing.T) {
	cwd := t.TempDir()
	cwd, _ = filepath.EvalSymlinks(cwd)
	sum := sha256.Sum256([]byte(cwd))
	hash := hex.EncodeToString(sum[:])
	root := t.TempDir()
	dir := filepath.Join(root, "tmp", "fixture", "chats")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	registry, _ := json.Marshal(map[string]any{"projects": map[string]string{cwd: "fixture"}})
	os.WriteFile(filepath.Join(root, "projects.json"), registry, 0600)
	text := strings.Join([]string{`{"sessionId":"selected","projectHash":"` + hash + `","lastUpdated":"2026-11-24T00:00:00Z"}`, `{"id":"a","type":"user","content":"initial"}`, `{"id":"b","type":"gemini","content":[{"text":"abandoned"}]}`, `{"$rewindTo":"b"}`, `{"id":"c","type":"gemini","content":[{"text":"bad"}]}`, `{"$patch":{"id":"c","content":[{"thought":true,"text":"private"},{"text":"corrected answer"}]}}`}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "session-native.jsonl"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Project: cwd, ConfigDir: root}
	res, err := a.Call(context.Background(), adapter.Request{Op: "read", Selection: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	v := res.(adapter.ReadResult)
	if len(v.Items) != 2 || v.Items[1].Text != "corrected answer" {
		t.Fatal(v)
	}
}
