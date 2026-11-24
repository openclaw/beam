package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRepackPreservesReviewedBytesAndIdentity(t *testing.T) {
	original := `{ "version":2,"beamId":"0123456789abcdef0123456789abcdef","source":"codex","title":"repair","updatedAt":"2025-12-01T12:00:00Z","completed":false,"items":[{"type":"userMessage","text":"hello"}] }`
	code, b, diag := invoke(t, App{}, original, "pack", "--from", "file", "--session", "-")
	if code != 0 || !bytes.Equal(b, []byte(original)) {
		t.Fatal(code, string(b), diag)
	}
	code, b, diag = invoke(t, App{}, strings.ReplaceAll(original, "hello", "my secret text"), "pack", "--from", "file", "--session", "-", "--redact", "secret")
	if code != 0 || !strings.Contains(string(b), "0123456789abcdef0123456789abcdef") || strings.Contains(string(b), "secret") {
		t.Fatal(code, string(b), diag)
	}
	code, b, diag = invoke(t, App{}, original, "convert", "-", "--packet-version", "1")
	if code != 0 || !bytes.Contains(b, []byte(`"version":1`)) {
		t.Fatal(code, string(b), diag)
	}
}
