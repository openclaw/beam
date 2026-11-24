//go:build beam_openclaw

package e2e

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureIssuer = "https://issuer.example.test"
const fixtureAudience = "beam-application"

type accessClaims struct {
	Issuer   string   `json:"iss"`
	Audience []string `json:"aud"`
	Subject  string   `json:"sub"`
	Expires  int64    `json:"exp"`
}

func fixtureAccessKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func issueFixtureAccess(t *testing.T, key *rsa.PrivateKey, sub, aud string, expiry time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(accessClaims{fixtureIssuer, []string{aud}, sub, expiry.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// The isolated edge trusts one ephemeral issuer. The origin receives identity
// only after a signature, issuer, audience, expiry and local subject mapping pass.
func fixtureAccessPerson(key *rsa.PublicKey, token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) {
		return ""
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ""
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature) != nil {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims accessClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != fixtureIssuer || !slices.Contains(claims.Audience, fixtureAudience) || claims.Expires <= time.Now().Unix() {
		return ""
	}
	return map[string]string{"publisher": "publisher@example.test", "teammate": "teammate@example.test"}[claims.Subject]
}

// The official helper enforces HTTPS and uses the OS trust store. Linux permits
// per-process SSL_CERT_FILE for this loopback fixture without changing host trust.
func TestOfficialCloudflaredCachedAccess(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("BEAM_E2E_CLOUDFLARED") == "" {
		t.Skip("requires enabled Linux cloudflared fixture")
	}
	if _, err := exec.LookPath("cloudflared"); err != nil {
		t.Fatal(err)
	}
	c := newCLI(t)
	key := fixtureAccessKey(t)
	token := issueFixtureAccess(t, key, "publisher", fixtureAudience, time.Now().Add(time.Hour))
	const domain = "edge.example.test"
	const route = "/team/api/v1/beam/sessions"
	var lookups, writes atomic.Int64
	edge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != route {
			t.Error("wrong mounted audience")
			w.WriteHeader(404)
			return
		}
		if r.Method == "HEAD" {
			lookups.Add(1)
			w.Header().Set("Cf-Access-Aud", fixtureAudience)
			w.Header().Set("Cf-Access-Domain", domain)
			w.WriteHeader(403)
			return
		}
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-origin" || fixtureAccessPerson(&key.PublicKey, r.Header.Get("Cf-Access-Token")) != "publisher@example.test" {
			w.WriteHeader(401)
			return
		}
		writes.Add(1)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer edge.Close()
	cert := filepath.Join(c.dir, "fixture-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: edge.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "SSL_CERT_FILE="+cert, "BEAM_AUTH_TOKEN=fixture-origin")
	var home string
	for _, v := range c.env {
		if s, ok := strings.CutPrefix(v, "HOME="); ok {
			home = s
		}
	}
	if home == "" {
		t.Fatal("missing fixture home")
	}
	dir := filepath.Join(home, ".cloudflared")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, domain+"-"+fixtureAudience+"-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	data := c.reviewed(t)
	file := filepath.Join(c.dir, "v1.json")
	c.run(t, data, "convert", "-", "--packet-version", "1", "--out", file)
	output, diag, err := c.command(t, nil, "share", file, "--to", edge.URL+route, "--cloudflare-login", "--json")
	if bytes.Contains(output, []byte(token)) || bytes.Contains(diag, []byte(token)) {
		t.Fatal("access credential exposed")
	}
	if err != nil || lookups.Load() != 1 || writes.Load() != 1 {
		t.Fatalf("official helper lookup=%d writes=%d success=%v", lookups.Load(), writes.Load(), err == nil)
	}
}
