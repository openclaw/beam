package receiver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

const fixtureID = "0123456789abcdef0123456789abcdef"

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := packet.Marshal(packet.Packet{Version: 2, BeamID: fixtureID, Source: "claude", Title: "fixture", UpdatedAt: "2026-11-24T01:00:00Z", Items: []packet.Item{{Type: "userMessage", Text: "reviewed context"}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func clientFor(t *testing.T, endpoint, state string) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: endpoint, StatePath: state})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func TestRejectRedirectAndForeignMachineLink(t *testing.T) {
	var requests atomic.Int64
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer foreign.Close()
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("CF-Access-Client-Secret") != "fixture" {
			t.Error("expected independent auth")
		}
		http.Redirect(w, r, foreign.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer home.Close()
	t.Setenv("BEAM_AUTH_TOKEN", "fixture")
	t.Setenv("CF_ACCESS_CLIENT_ID", "test")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "fixture")
	t.Setenv("BEAM_ACCESS_TOKEN", "")
	headers, err := EnvHeaders()
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{Endpoint: home.URL, Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Capabilities(context.Background()); !asHTTP(err, 307) {
		t.Fatal(err)
	}
	if _, err := c.Pull(context.Background(), foreign.URL+"/api/v2/beam/publications/a"); err == nil {
		t.Fatal("accepted foreign endpoint")
	}
	if requests.Load() != 0 {
		t.Fatal("forwarded credentials")
	}
}
func TestReceiverLostReleaseResponse(t *testing.T) {
	data := fixture(t)
	state := filepath.Join(t.TempDir(), "state.sqlite")
	ctx := context.Background()
	const generation = "generation.one"
	var creates, releases atomic.Int64
	var releaseKey string
	url := "/team/beam/example-" + fixtureID[:12]
	api := "/team" + v2Path + "/publications/" + generation
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/team" + v2Path + "/capabilities":
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["publish","read","release","resolve","continue"],"maxBodyBytes":57344,"sendReceiptRetentionSeconds":86400}`)
		case "/team" + v2Path + "/snapshots/" + fixtureID + "/head":
			w.WriteHeader(404)
		case "/team" + v2Path + "/snapshots/" + fixtureID:
			creates.Add(1)
			if r.Method != "PUT" || r.Header.Get("If-None-Match") != "*" || !uuidRE.MatchString(r.Header.Get("Idempotency-Key")) {
				t.Error("bad publish conditions")
			}
			json.NewEncoder(w).Encode(Publication{BeamID: fixtureID, PublicationID: generation, Revision: "\"rev\"", Digest: packet.Digest(data), Disposition: "applied", CanRelease: true, URL: url, APIURL: api})
		case api + "/release":
			if r.Header.Get("If-Match") != "\"rev\"" {
				t.Error("no release revision")
			}
			if releases.Add(1) == 1 {
				releaseKey = r.Header.Get("Idempotency-Key")
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
				return
			}
			if r.Header.Get("Idempotency-Key") != releaseKey {
				t.Error("regenerated ambiguous release")
			}
			json.NewEncoder(w).Encode(Release{PublicationID: generation, Revision: "\"rev\"", State: "released", URL: url})
		case api + "/snapshot":
			w.Header().Set("ETag", "\"rev\"")
			w.Header().Set("Beam-Digest", packet.Digest(data))
			w.Write(data)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := clientFor(t, srv.URL+"/team", state)
	if _, err := c.Handoff(ctx, fixtureID, data); err == nil {
		t.Fatal("lost ACK did not fail")
	}
	c.Close()
	c = clientFor(t, srv.URL+"/team", state)
	result, err := c.Handoff(ctx, fixtureID, nil)
	if err != nil || result.PublicationID != generation {
		t.Fatalf("retry %+v %v", result, err)
	}
	if creates.Load() != 1 || releases.Load() != 2 {
		t.Fatal("failed to recover without duplicate")
	}
	row, _, err := c.LegacyRow(ctx, fixtureID)
	if err != nil || row.Receipt != nil || row.Payload != nil {
		t.Fatal("legacy fence invalid")
	}
	// A new device has no sender journal. The full machine link is sufficient.
	other := clientFor(t, srv.URL+"/team", filepath.Join(t.TempDir(), "new.sqlite"))
	read, err := other.Pull(ctx, result.APIURL)
	if err != nil || string(read) != string(data) {
		t.Fatalf("pull: %v", err)
	}
}

func TestLegacySnapshotOnlyAndTransferredHooks(t *testing.T) {
	raw, err := packet.V1Projection(func() packet.Packet { p, _ := packet.Parse(fixture(t)); return p }())
	if err != nil {
		t.Fatal(err)
	}
	var handoff atomic.Bool
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != v1Path {
			t.Error(r.URL.Path)
		}
		if handoff.Load() {
			w.WriteHeader(409)
			fmt.Fprintf(w, `{"ok":false,"beamId":%q,"url":"/beam/0123456789ab","handoff":{"owner":"gateway","access":"shared","id":%q}}`, fixtureID, strings.Repeat("f", 32))
			return
		}
		fmt.Fprint(w, `{"ok":true,"beamId":"untrusted"}`)
	}))
	defer srv.Close()
	c := clientFor(t, srv.URL+v1Path, filepath.Join(t.TempDir(), "state.sqlite"))
	r, err := c.HandoffV1(context.Background(), fixtureID, raw, true)
	if err != nil || !r.SnapshotFallback || r.BeamID != fixtureID {
		t.Fatalf("snapshot compatibility: %+v %v", r, err)
	}
	if _, ok, _ := c.LegacyRow(context.Background(), fixtureID); ok {
		t.Fatal("snapshot fallback retired source")
	}
	handoff.Store(true)
	r, err = c.ShareV1(context.Background(), raw)
	if err != nil || r.Handoff == nil {
		t.Fatalf("expected transferred receipt: %+v %v", r, err)
	}
	if _, err = c.ShareV1(context.Background(), raw); err != nil || calls.Load() != 2 {
		t.Fatal("retired hook issued another upload", err)
	}
}

func TestLegacyReleasePreservesFrozenBytes(t *testing.T) {
	ctx := context.Background()
	p, _ := packet.Parse(fixture(t))
	encoded, err := packet.V1Projection(p)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(strings.Replace(string(encoded), "reviewed context", "<reviewed & frozen>", 1))
	generation := strings.Repeat("f", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) == string(raw) {
			fmt.Fprintf(w, `{"publicationId":%q}`, generation)
			return
		}
		end := strings.LastIndexByte(string(raw), '}')
		expected := string(raw[:end]) + `,"handoffId":"` + generation + `"` + string(raw[end:])
		if string(body) != expected {
			t.Error("release changed the stored snapshot bytes")
		}
		fmt.Fprintf(w, `{"beamId":%q,"url":"/beam/0123456789ab","handoff":{"owner":"gateway","access":"owned","id":%q}}`, fixtureID, generation)
	}))
	defer srv.Close()
	c := clientFor(t, srv.URL+v1Path, filepath.Join(t.TempDir(), "state.db"))
	if _, err := c.HandoffV1(ctx, fixtureID, raw, false); err != nil {
		t.Fatal(err)
	}
}
