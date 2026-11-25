package cli

import (
	"bytes"
	"slices"

	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

func TestReviewBindsVisibleSelectionToFrozenBytes(t *testing.T) {
	candidate := `{"source":"fixture","origin":{"adapter":"fixture","sourceHome":"local","nativeSession":"s"},"title":"Example","updatedAt":"2026-01-02T03:04:05Z","completed":true,"completeness":"complete","items":[{"type":"other","text":"<system-reminder>hidden</system-reminder>"},{"type":"userMessage","text":"Private detail","speakerId":"alice"},{"type":"agentMessage","text":"Keep the decision"},{"type":"userMessage","text":"Verify","speakerId":"bob"}]}`
	code, raw, diag := invoke(t, App{}, candidate, "clean", "--protocol", "1")
	if code != 0 {
		t.Fatal(diag)
	}
	code, preview, diag := invoke(t, App{}, string(raw), "review", "-")
	if code != 0 || !strings.Contains(string(preview), `1  userMessage p1  "Private detail"`) || strings.Contains(string(preview), "hidden") {
		t.Fatalf("%d %s %s", code, preview, diag)
	}
	path := filepath.Join(t.TempDir(), "edited.json")
	digest := packet.Digest(raw)
	for _, flags := range [][]string{{"--omit", "1"}, {"--keep", "3", "--keep", "2"}} {
		args := append([]string{"review", "-", "--expect-digest", digest, "--out", path, "--json"}, flags...)
		code, receipt, diag := invoke(t, App{}, string(raw), args...)
		got, err := os.ReadFile(path)
		if err != nil || code != 0 || !strings.Contains(string(receipt), packet.Digest(got)) || !strings.Contains(string(receipt), digest) {
			t.Fatalf("%d %s %v %s", code, receipt, err, diag)
		}
		p, err := packet.Parse(got)
		if err != nil || len(p.Items) != 2 || p.Items[0].Text != "Keep the decision" || p.Items[1].Speaker != "p2" {
			t.Fatal(p, err)
		}
	}
	for _, flags := range [][]string{{"--omit", "1"}, {"--keep", "4", "--expect-digest", digest}, {"--omit", "1", "--keep", "2", "--expect-digest", digest}, {"--omit", "1", "--omit", "2", "--omit", "3", "--expect-digest", digest}, {"--keep", "1", "--expect-digest", "old"}} {
		if err := os.WriteFile(path, []byte("untouched"), 0600); err != nil {
			t.Fatal(err)
		}
		code, _, _ := invoke(t, App{}, string(raw), append([]string{"review", "-", "--out", path}, flags...)...)
		got, _ := os.ReadFile(path)
		if code == 0 || string(got) != "untouched" {
			t.Fatal(flags, code, string(got))
		}
	}
	code, b, _ := invoke(t, App{}, string(raw), "review", "-", "--keep", "2", "--out", "-", "--expect-digest", digest, "--json")
	var result struct {
		Result struct {
			Packet json.RawMessage
			Digest string
		}
	}
	if code != 0 || json.Unmarshal(b, &result) != nil || packet.Digest(result.Result.Packet) != result.Result.Digest {
		t.Fatal(string(b))
	}
}

func TestReviewPortableEscapesAndProtectedReceiptBytes(t *testing.T) {
	// A third-party writer may use literal HTML characters and JS separators
	// in JSON. Escaped backslashes are data and must survive compaction.
	text := strings.Repeat("<&>\u2028\u2029", 950) + `\u003c\\u0026`
	items := []packet.Item{
		{Type: "other", Text: text}, {Type: "other", Text: text}, {Type: "other", Text: text}, {Type: "other", Text: "omit"},
	}
	p := packet.Packet{Version: 2, BeamID: "0123456789abcdef0123456789abcdef", Source: "file", Title: "Portable", UpdatedAt: "2026-01-01T00:00:00Z", Items: items}
	// Encode the external fixture without relying on Beam's packet writer.
	var encoded bytes.Buffer
	enc := json.NewEncoder(&encoded)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(p); err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimSpace(encoded.String())
	for _, flags := range [][]string{nil, {"--omit", "4", "--out", "-", "--expect-digest", packet.Digest([]byte(raw))}} {
		args := append([]string{"review", "-", "--json"}, flags...)
		code, b, diag := invoke(t, App{}, raw, args...)
		var receipt struct {
			Result struct {
				Packet json.RawMessage
				Digest string
			}
		}
		if code != 0 || json.Unmarshal(b, &receipt) != nil {
			t.Fatalf("review failed: %d %s", code, diag)
		}
		wire := receipt.Result.Packet
		got, err := packet.Parse(wire)
		if err != nil || packet.Digest(wire) != receipt.Result.Digest || got.Items[0].Text != text {
			t.Fatalf("changed packet (%d bytes): %v", len(wire), err)
		}
		if flags == nil {
			if !bytes.Equal(wire, []byte(raw)) {
				t.Fatal("preview rewrote original bytes")
			}
			continue
		}
		for _, op := range []string{"openCopy", "stageReference"} {
			encoded, err := adapter.EncodeRequest(adapter.Request{Op: op, OperationID: "fixture", Packet: got})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := adapter.DecodeRequest(encoded)
			if err != nil || !slices.Equal(decoded.Packet.Items, items[:3]) {
				t.Fatalf("%s lost fixture items: %v", op, err)
			}
		}
	}
}
