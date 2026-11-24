package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func TestCommandsMachineLinksAndNamedDestination(t *testing.T) {
	data := fixture(t)
	view := "/test/beam/fixture-" + fixtureID[:12]
	api := "/test" + v2Path + "/publications/frozen"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/test" + v2Path + "/capabilities":
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["read","publish"],"maxBodyBytes":57344}`)
		case "/test" + v2Path + "/snapshots/" + fixtureID + "/head":
			w.WriteHeader(404)
		case "/test" + v2Path + "/snapshots/" + fixtureID:
			json.NewEncoder(w).Encode(Publication{BeamID: fixtureID, PublicationID: "frozen", Revision: "\"v\"", Digest: packet.Digest(data), Disposition: "applied", URL: view, APIURL: api})
		case api + "/snapshot":
			w.Header().Set("ETag", "\"v\"")
			w.Header().Set("Beam-Digest", packet.Digest(data))
			w.Write(data)
		default:
			t.Error(r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("BEAM_ENDPOINT", "")
	t.Setenv("BEAM_STATE", filepath.Join(dir, "state.sqlite"))
	for _, k := range []string{"BEAM_AUTH_TOKEN", "BEAM_ACCESS_TOKEN", "CF_ACCESS_CLIENT_ID", "CF_ACCESS_CLIENT_SECRET"} {
		t.Setenv(k, "")
	}
	if err := os.Mkdir(filepath.Join(dir, "beam"), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{"team": srv.URL + "/test"})
	if err := os.WriteFile(filepath.Join(dir, "beam", "receivers.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	ctx := context.Background()
	code, err := Run(ctx, "share", []string{"-", "--to", "team", "--json"}, bytes.NewReader(data), &out, &stderr)
	if err != nil || code != 0 {
		t.Fatal(code, err, out.String(), stderr.String())
	}
	var reply struct {
		OK     bool
		Result Publication
	}
	if json.Unmarshal(out.Bytes(), &reply) != nil || !reply.OK || reply.Result.APIURL != srv.URL+api {
		t.Fatal("invalid machine receipt")
	}
	out.Reset()
	dst := filepath.Join(dir, "out.beam.json")
	code, err = Run(ctx, "pull", []string{reply.Result.APIURL, "--endpoint", "team", "--out", dst}, strings.NewReader(""), &out, &stderr)
	disk, readErr := os.ReadFile(dst)
	if err != nil || code != 0 || readErr != nil || !bytes.Equal(disk, data) {
		t.Fatal("pull changed reviewed bytes", code, err, out.String())
	}
	out.Reset()
	code, err = Run(ctx, "handoff", []string{"-", "--to", "team", "--json"}, bytes.NewReader(data), &out, &stderr)
	if err != nil || code != 3 || !strings.Contains(out.String(), "unsupported_capability") {
		t.Fatal("missing capability silently accepted", code, err, out.String())
	}
	c := clientFor(t, srv.URL+"/test", filepath.Join(dir, "state.sqlite"))
	if _, found, _ := c.LegacyRow(ctx, fixtureID); found {
		t.Fatal("preflight retired ineligible source")
	}
}

type routeFixture struct {
	cursor    bool
	access    bool
	listCalls int
}

func (s *routeFixture) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == "sessions.catalog.route" {
		if !s.access {
			return json.RawMessage("null"), nil
		}
		return json.RawMessage(`{"catalogId":"beam","routeSegment":"beam","methods":{"list":"beam.owned.list"}}`), nil
	}
	if method != "beam.owned.list" {
		return nil, fmt.Errorf("unexpected method %s", method)
	}
	s.listCalls++
	cursor := ""
	if s.cursor {
		cursor = "more"
	}
	return json.Marshal(map[string]any{"catalogs": []any{map[string]any{"id": "beam", "shareRoute": map[string]string{"routeSegment": "beam", "hostId": "gateway"}, "hosts": []any{map[string]any{"hostId": "gateway", "nextCursor": cursor, "sessions": []any{map[string]string{"threadId": fixtureID}}}}}}})
}
func TestColdShortLinkUsesScopedLookupAndRejectsPartialMatch(t *testing.T) {
	ctx := context.Background()
	s := &routeFixture{access: true}
	id, err := resolvePrefix(ctx, s, fixtureID[:12])
	if err != nil || id != fixtureID {
		t.Fatal(id, err)
	}
	s.cursor = true
	if _, err := resolvePrefix(ctx, s, fixtureID[:12]); err == nil {
		t.Fatal("partial match accepted")
	}
	s.access = false
	if _, err := resolvePrefix(ctx, s, fixtureID[:12]); err == nil || s.listCalls != 2 {
		t.Fatal("public fallback for restricted user", err, s.listCalls)
	}
}

func TestCloudflaredShimOnlyWhenRequested(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX synthetic cloudflared fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cloudflared"), []byte("#!/bin/sh\ncase \"$1 $2 $3\" in\n 'access token --app=https://gateway.example') echo synthetic-token;;\n *) exit 2;;\nesac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c, err := New(Config{Endpoint: "https://gateway.example/team", CloudflareLogin: true, Headers: http.Header{"Authorization": {"Bearer synthetic-origin"}}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.authHeaders(context.Background())
	if err != nil || h.Get("CF-Access-Token") != "synthetic-token" || h.Get("Authorization") != "Bearer synthetic-origin" {
		t.Fatal("failed scoped edge+origin auth")
	}
	other, err := New(Config{Endpoint: "https://gateway.example/team"})
	if err != nil {
		t.Fatal(err)
	}
	h, err = other.authHeaders(context.Background())
	if err != nil || len(h) != 0 {
		t.Fatal("implicit cloudflare login")
	}
}
