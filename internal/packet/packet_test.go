package packet

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const example = `{"version":2,"beamId":"0123456789abcdef0123456789abcdef","source":"codex","title":"Example","updatedAt":"2024-02-29T01:02:03Z","completed":false,"items":[{"type":"userMessage","text":"Hi","speaker":"p1"}]}`

func TestPacketBoundary(t *testing.T) {
	for _, test := range []struct{ from, to string }{
		{`"title":`, `"title":"a","title":`}, {`"type":`, `"type":"other","type":`},
		{`"title":`, `"\u0074itle":"a","title":`},
		{`"title":`, `"Title":`}, {`"Hi"`, `null`}, {`"speaker":"p1"`, `"speaker":""`},
		{`"completed":false,`, ""}, {`"source":"codex"`, `"source":"CODEX"`},
		{`"userMessage"`, `"agentMessage"`}, {`"p1"`, `"p201"`},
		{`"Hi"`, `"\ud800"`}, {`"Hi"`, `"\udc00"`}, {`"Hi"`, `"  "`},
		{`2024-02-29`, `2023-02-29`}, {`03Z`, `03+26:60`},
		{`"Example"`, `"` + strings.Repeat("🦊", 81) + `"`},
		{`"Hi"`, `"` + strings.Repeat("🦊", 3001) + `"`},
		{`"version":2`, `"version":2,"handoffId":"0123456789abcdef0123456789abcdef"`},
	} {
		if _, err := Parse([]byte(strings.Replace(example, test.from, test.to, 1))); err == nil {
			t.Errorf("accepted replacement of %s with %s", test.from, Clip(test.to, 32))
		}
	}
	for _, bad := range [][]byte{append([]byte(example), 0xff), append([]byte(example), []byte(example)...), bytes.Repeat([]byte{' '}, MaxBytes+1)} {
		if _, err := Parse(bad); err == nil {
			t.Error("accepted invalid JSON")
		}
	}
	valid := strings.Replace(example, `"Hi"`, `"\ud83e\udd8a"`, 1)
	p, err := Parse([]byte(valid))
	if err != nil || p.Items[0].Text != "🦊" {
		t.Fatalf("valid astral pair: %v", err)
	}
	if _, err = Parse([]byte(strings.Replace(example, `"Hi"`, `"\\uD800"`, 1))); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyProjection(t *testing.T) {
	p, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	b, err := V1Projection(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("speaker")) || bytes.Contains(b, []byte("handoffId")) {
		t.Fatal("legacy projection changed authority or retained participant alias")
	}
	withControl := strings.Replace(string(b), `"version":1,`, `"version":1,"handoffId":"0123456789abcdef0123456789abcdef","hookEvent":"Stop",`, 1)
	copy, err := ImportV1([]byte(withControl))
	if err != nil || copy.BeamID != p.BeamID || copy.Version != 2 {
		t.Fatalf("legacy import: %v", err)
	}
	if strings.Contains(Render(copy), "Stop") {
		t.Fatal("transport control leaked into reference")
	}
}

func TestDigestAndRender(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Marshal(p)
	if err != nil || !bytes.Equal(encoded, raw) {
		t.Fatalf("golden bytes changed: %v", err)
	}
	golden, err := os.ReadFile("../../conformance/packet/example.sha256")
	if err != nil || Digest(raw) != "sha256:"+strings.TrimSpace(string(golden)) {
		t.Fatalf("golden digest: %v", err)
	}
	if got := SourceID("codex", "fixture-home", "session-1", ""); got != p.BeamID {
		t.Fatalf("origin vector got %s", got)
	}
	if SourceID("ab", "c", "d", "") == SourceID("a", "bc", "d", "") || SourceID("codex", "fixture-home", "session-1", "fork") == p.BeamID {
		t.Fatal("origin tuple collision")
	}
	p.Items[0].Text = "a\r\n![image](https://example.test)\n> quit"
	if !strings.Contains(Render(p), ">     ![image]") || strings.Contains(Render(p), "\r") {
		t.Fatal("reference contains an active embedded image or carriage return")
	}
}

func TestSharedValidatorVectors(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := os.ReadFile("../../conformance/packet/validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var tests []struct {
		Name, From, To string
		Valid          bool
	}
	if err = json.Unmarshal(vectors, &tests); err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			if !bytes.Contains(raw, []byte(tc.From)) {
				t.Fatal("fixture no longer contains input")
			}
			_, err := Parse(bytes.Replace(raw, []byte(tc.From), []byte(tc.To), 1))
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v error=%v", tc.Valid, err)
			}
		})
	}
}

func TestIdentityVectors(t *testing.T) {
	data, err := os.ReadFile("../../conformance/packet/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		V2 []struct {
			Tuple [4]string
			ID    string
		}
		V1CLI    []struct{ Source, Identity, ID string }
		V1Mirror []struct{ CatalogID, HostID, ThreadID, ID string }
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors.V2 {
		if SourceID(v.Tuple[0], v.Tuple[1], v.Tuple[2], v.Tuple[3]) != v.ID {
			t.Error("v2 identity changed")
		}
	}
	for _, v := range vectors.V1CLI {
		if LegacySourceID(v.Source, v.Identity) != v.ID {
			t.Error("v1 CLI identity changed")
		}
	}
	for _, v := range vectors.V1Mirror {
		if LegacyMirrorID(v.CatalogID, v.HostID, v.ThreadID) != v.ID {
			t.Error("v1 mirror identity changed")
		}
	}
}
