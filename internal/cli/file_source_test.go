package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/packet"
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

func TestPacketReceiptMatchesBytesWrittenByCommand(t *testing.T) {
	fixture := `{ "version":2,"beamId":"0123456789abcdef0123456789abcdef","source":"file","title":"A & B","updatedAt":"2025-12-01T12:00:00Z","completed":false,"items":[{"type":"userMessage","text":"hello"}] }`
	for _, args := range [][]string{
		{"inspect", "-"},
		{"pack", "--from", "file", "--session", "-"},
		{"push", "--dry-run", "--from", "file", "--session", "-"},
		{"convert", "-", "--packet-version", "1"},
		{"convert", "-", "--packet-version", "2"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			input := fixture
			if args[0] == "convert" && args[len(args)-1] == "2" {
				// Plain JSON fits, while HTML-escaping every ampersand would exceed 56 KiB.
				item := `{"type":"userMessage","text":"` + strings.Repeat("&", 6000) + `"}`
				input = strings.Replace(input, `{"type":"userMessage","text":"hello"}`, strings.Join([]string{item, item, item}, ","), 1)
			}
			code, raw, diag := invoke(t, App{}, input, args...)
			if code != 0 {
				t.Fatalf("writing packet failed: %d %s", code, diag)
			}
			code, out, diag := invoke(t, App{}, input, append(args, "--json")...)
			var receipt struct {
				OK     bool
				Result struct {
					Packet json.RawMessage
					Digest string
					Bytes  *int
				}
			}
			if code != 0 || json.Unmarshal(out, &receipt) != nil || !receipt.OK {
				t.Fatalf("writing receipt failed: %d %s", code, diag)
			}
			if !bytes.Equal(raw, receipt.Result.Packet) || receipt.Result.Digest != packet.Digest(raw) {
				t.Fatalf("receipt differs from command output: packet %d bytes, receipt packet %d bytes", len(raw), len(receipt.Result.Packet))
			}
			if n := receipt.Result.Bytes; n != nil && *n != len(raw) {
				t.Fatalf("receipt reports %d bytes, want %d", *n, len(raw))
			}
		})
	}
}
