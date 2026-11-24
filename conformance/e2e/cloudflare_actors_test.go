//go:build beam_openclaw

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func withAccess(c cli, token string) cli {
	c.env = append(slices.Clone(c.env), "BEAM_ACCESS_TOKEN="+token)
	return c
}

func TestSignedAccessActorsOnOpenClaw(t *testing.T) {
	c := newCLI(t)
	model := httptest.NewServer(new(modelCalls))
	defer model.Close()
	g := newGateway(t, c, model.URL)
	g.enableBeam(t)
	g.start(t)
	key := fixtureAccessKey(t)
	origin, _ := url.Parse("http://127.0.0.1:" + g.port)
	proxy := &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
		token := req.In.Header.Get("Cf-Access-Token")
		req.SetURL(origin)
		req.Out.Header.Del("Cf-Access-Token")
		req.Out.Header.Set("Cf-Access-Jwt-Assertion", token)
		req.Out.Header.Set("X-Forwarded-User", fixtureAccessPerson(&key.PublicKey, token))
		req.Out.Header.Set("X-Forwarded-Proto", "https")
		req.Out.Header.Set("X-Forwarded-For", "192.0.2.10")
	}}
	var forwarded atomic.Int64
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureAccessPerson(&key.PublicKey, r.Header.Get("Cf-Access-Token")) == "" {
			w.WriteHeader(401)
			return
		}
		forwarded.Add(1)
		proxy.ServeHTTP(w, r)
	}))
	defer edge.Close()
	packet := filepath.Join(c.dir, "reviewed.beam.json")
	if err := os.WriteFile(packet, c.reviewed(t), 0600); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	owner := issueFixtureAccess(t, key, "publisher", fixtureAudience, expiry)
	peer := issueFixtureAccess(t, key, "teammate", fixtureAudience, expiry)
	for _, bad := range []string{"", issueFixtureAccess(t, key, "publisher", "other-app", expiry), issueFixtureAccess(t, key, "publisher", fixtureAudience, time.Now().Add(-time.Hour)), issueFixtureAccess(t, fixtureAccessKey(t), "publisher", fixtureAudience, expiry)} {
		out, diag, err := withAccess(c, bad).command(t, nil, "share", packet, "--to", edge.URL, "--json")
		if err == nil || (bad != "" && (bytes.Contains(out, []byte(bad)) || bytes.Contains(diag, []byte(bad)))) {
			t.Fatal("invalid edge identity accepted or exposed")
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("unverified identity reached Gateway")
	}
	var pub struct {
		APIURL           string
		CanRelease       bool
		Revision, Digest string
	}
	db := filepath.Join(c.dir, "publisher.sqlite")
	machineResult(t, withAccess(c, owner).run(t, nil, "share", packet, "--to", edge.URL, "--state", db, "--json"), &pub)
	if !pub.CanRelease || pub.APIURL == "" {
		t.Fatal("verified publisher did not own publication")
	}
	got := withAccess(c, peer).run(t, nil, "pull", pub.APIURL, "--to", edge.URL)
	want, err := os.ReadFile(packet)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("authorized peer failed to read reviewed bytes")
	}
	body, _ := json.Marshal(map[string]string{"digest": pub.Digest})
	req, err := http.NewRequestWithContext(t.Context(), "POST", pub.APIURL+"/release", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", pub.Revision)
	req.Header.Set("Idempotency-Key", "10000000-0000-4000-8000-000000000001")
	req.Header.Set("Cf-Access-Token", peer)
	req.Header.Set("X-Forwarded-User", "publisher@example.test")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 && res.StatusCode != 404 {
		t.Fatalf("peer release status %d", res.StatusCode)
	}
	withAccess(c, owner).run(t, nil, "handoff", packet, "--to", edge.URL, "--state", db, "--json")
}
