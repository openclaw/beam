package legacy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef"
const generation = "11111111111111111111111111111111"
const testPacket = `{"version":1,"beamId":"0123456789abcdef0123456789abcdef","source":"claude","title":"Example","updatedAt":"2026-11-24T08:00:00.000Z","completed":false,"items":[{"type":"userMessage","text":"Please check the CSV quoting."}]}`

func call(t *testing.T, req request) (receipt, diag string, code int) {
	t.Helper()
	input, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code = Run(context.Background(), nil, bytes.NewReader(input), &out, &stderr)
	return out.String(), stderr.String(), code
}

func TestHandoffSurvivesLostReleaseAndRemovalOfSource(t *testing.T) {
	t.Setenv("BEAM_ACCESS_TOKEN", "")
	t.Setenv("BEAM_AUTH_TOKEN", "")
	var posted []map[string]any
	failOnce := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		posted = append(posted, body)
		w.Header().Set("Content-Type", "application/json")
		if _, releasing := body["handoffId"]; releasing {
			if failOnce {
				failOnce = false
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":"uncertain"}`)
				return
			}
			fmt.Fprintf(w, `{"ok":true,"beamId":%q,"url":"/beam/%s","handoff":{"id":%q,"owner":"gateway","access":"owned"}}`, testID, testID[:12], generation)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"beamId":%q,"url":"/beam/%s","publicationId":%q}`, testID, testID[:12], generation)
	}))
	defer server.Close()
	req := request{Operation: "handoff", BeamID: testID, Packet: testPacket, State: filepath.Join(t.TempDir(), "handoffs.sqlite"), Endpoint: server.URL + "/api/v1/beam/sessions"}
	if out, _, code := call(t, req); code != 1 || out != "" {
		t.Fatalf("ambiguous release should fail with no receipt: %q %d", out, code)
	}
	// Recover through the old --beam-id interface with no source transcript.
	req.Packet = ""
	out, diag, code := call(t, req)
	if code != 0 || !strings.Contains(out, generation) {
		t.Fatalf("retry: %q %q %d", out, diag, code)
	}
	if len(posted) != 3 || posted[0]["handoffId"] != nil || posted[1]["handoffId"] != generation || posted[2]["handoffId"] != generation {
		t.Fatalf("unexpected publish/release sequence: %v", posted)
	}
	req.Operation = "share"
	req.Hook = true
	req.Packet = testPacket
	if _, diag, code = call(t, req); code != 0 || len(posted) != 3 {
		t.Fatalf("acknowledged hook made a new upload: %s, sent %d requests", diag, len(posted))
	}
}

func TestUnattendedHookAndEnvelopeValidation(t *testing.T) {
	t.Setenv("BEAM_AUTH_TOKEN", "")
	t.Setenv("BEAM_ACCESS_TOKEN", "")
	req := request{Operation: "share", Hook: true, BeamID: testID, State: filepath.Join(t.TempDir(), "handoffs.sqlite"), Packet: testPacket, Endpoint: "https://beam.invalid/api/v1/beam/sessions"}
	out, diag, code := call(t, req)
	if out != "" || code != 1 || !strings.Contains(diag, "no Beam credentials configured") {
		t.Fatalf("unattended hook: %q %q %d", out, diag, code)
	}
	req.Hook = false
	req.Operation = "read"
	req.Packet = ""
	if out, diag, code = call(t, req); code != 1 || out != "" || !strings.Contains(diag, "ownership transfer") {
		t.Fatalf("missing receipt: %q %q %d", out, diag, code)
	}
	req.Operation = "handoff"
	req.Packet = strings.Replace(testPacket, `"source":"claude"`, `"source":"claude","handoffId":"11111111111111111111111111111111"`, 1)
	if _, _, code = call(t, req); code != 1 {
		t.Fatal("accepted caller-supplied handoff control")
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--protocol"}, strings.NewReader(""), &stdout, &stderr); code != 0 || stdout.String() != "1\n" {
		t.Fatal("invalid skill protocol probe")
	}
}
