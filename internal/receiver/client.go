// Package receiver implements authenticated Beam publication, release and recovery.
package receiver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/openclaw/beam/internal/journal"
)

const v1Path = "/api/v1/beam/sessions"
const v2Path = "/api/v2/beam"
const maxResponse = 64 * 1024

var beamIDRE = regexp.MustCompile(`^[a-f0-9]{32}$`)
var opaqueRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._~-]{0,255}$`)
var uuidRE = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type Config struct {
	Endpoint, StatePath string
	HTTPClient          *http.Client
	Headers             http.Header
	CloudflareLogin     bool
}

type Client struct {
	base           *url.URL
	legacyEndpoint string
	legacy         bool
	http           *http.Client
	headers        http.Header
	login          bool
	state          string
	mu             sync.Mutex
	store          *journal.Store
}

type Error struct {
	Code  string
	HTTP  int
	Retry string
}

func (e *Error) Error() string          { return "Beam receiver: " + e.Code }
func failure(code, retry string) *Error { return &Error{Code: code, Retry: retry} }

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(cfg.Endpoint, "#") {
		return nil, failure("invalid_endpoint", "never")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, failure("https_required", "never")
	}
	if strings.Contains(u.EscapedPath(), "%") && strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || strings.Contains(u.Path, "\\") {
		return nil, failure("invalid_endpoint", "never")
	}
	for _, piece := range strings.Split(u.Path, "/") {
		if piece == "." || piece == ".." {
			return nil, failure("invalid_endpoint", "never")
		}
	}
	legacy := strings.HasSuffix(u.Path, v1Path)
	legacyEndpoint := u.String()
	u.Path = strings.TrimSuffix(u.Path, "/")
	if legacy {
		u.Path = strings.TrimSuffix(u.Path, v1Path)
	} else {
		u.Path = strings.TrimSuffix(u.Path, v2Path)
	}
	u.RawPath = ""
	if !legacy {
		legacyEndpoint = endpointURL(u, v1Path)
	}
	h := &http.Client{Timeout: 45 * time.Second}
	if cfg.HTTPClient != nil {
		copy := *cfg.HTTPClient
		h = &copy
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	headers := make(http.Header)
	for k, values := range cfg.Headers {
		key := http.CanonicalHeaderKey(k)
		if !slices.Contains([]string{"Authorization", "Cf-Access-Token", "Cf-Access-Client-Id", "Cf-Access-Client-Secret"}, key) {
			return nil, failure("unsupported_auth_header", "never")
		}
		if len(values) != 1 || strings.ContainsAny(values[0], "\r\n") {
			return nil, failure("invalid_auth_header", "never")
		}
		headers.Set(key, values[0])
	}
	return &Client{base: u, legacyEndpoint: legacyEndpoint, legacy: legacy, http: h, headers: headers, login: cfg.CloudflareLogin, state: cfg.StatePath}, nil
}

func EnvHeaders() (http.Header, error) {
	h := http.Header{}
	if v := os.Getenv("BEAM_AUTH_TOKEN"); v != "" {
		h.Set("Authorization", "Bearer "+v)
	}
	if v := os.Getenv("BEAM_ACCESS_TOKEN"); v != "" {
		h.Set("CF-Access-Token", v)
	}
	id, secret := os.Getenv("CF_ACCESS_CLIENT_ID"), os.Getenv("CF_ACCESS_CLIENT_SECRET")
	if (id == "") != (secret == "") || (id != "" && h.Get("CF-Access-Token") != "") {
		return nil, failure("ambiguous_edge_credentials", "never")
	}
	if id != "" {
		h.Set("CF-Access-Client-Id", id)
		h.Set("CF-Access-Client-Secret", secret)
	}
	return h, nil
}
func endpointURL(base *url.URL, suffix string) string {
	u := *base
	u.Path += suffix
	return u.String()
}

func (c *Client) authHeaders(ctx context.Context) (http.Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.login && c.headers.Get("Cf-Access-Token") == "" && c.headers.Get("Cf-Access-Client-Id") == "" {
		// Access may assign a distinct audience to the Beam API under a
		// shared host. Discover it at the selected mounted endpoint.
		app := endpointURL(c.base, v2Path+"/capabilities")
		if c.legacy {
			app = c.legacyEndpoint
		}
		token := func() (string, error) {
			out, err := exec.CommandContext(ctx, "cloudflared", "access", "token", "--app="+app).Output()
			if err != nil || len(out) > 32*1024 {
				return "", failure("cloudflare_token_unavailable", "never")
			}
			value := strings.TrimSpace(string(out))
			if value == "" || strings.ContainsAny(value, "\r\n") {
				return "", failure("cloudflare_token_unavailable", "never")
			}
			return value, nil
		}
		t, err := token()
		if err != nil {
			cmd := exec.CommandContext(ctx, "cloudflared", "access", "login", "--quiet", app)
			cmd.Stdin = os.Stdin
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return nil, failure("cloudflare_login_failed", "never")
			}
			t, err = token()
			if err != nil {
				return nil, err
			}
		}
		c.headers.Set("CF-Access-Token", t)
	}
	return c.headers.Clone(), nil
}
func (c *Client) hasJournal() (bool, error) {
	path := c.state
	if path == "" {
		var err error
		path, err = journal.DefaultPath()
		if err != nil {
			return false, err
		}
	}
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) journal() (*journal.Store, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		s, err := journal.Open(c.state)
		if err != nil {
			return nil, err
		}
		c.store = s
	}
	return c.store, nil
}
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store != nil {
		err := c.store.Close()
		c.store = nil
		return err
	}
	return nil
}

type response struct {
	body   []byte
	header http.Header
	status int
}

func (c *Client) request(ctx context.Context, method, suffix string, body []byte, headers http.Header) (response, error) {
	h, err := c.authHeaders(ctx)
	if err != nil {
		return response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpointURL(c.base, suffix), bytes.NewReader(body))
	if err != nil {
		return response{}, failure("invalid_request", "never")
	}
	req.Header = h
	// The durable journal owns write retries after a lost acknowledgement. Disable
	// the transport's implicit Idempotency-Key replay on reused connections.
	if body != nil {
		req.GetBody = nil
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header[k] = v
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, failure("transport_failed", "same_request")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return response{}, failure("incomplete_response", "same_request")
	}
	if len(b) > maxResponse {
		return response{}, failure("oversize_response", "after_reconcile")
	}
	out := response{b, res.Header, res.StatusCode}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, httpFailure(res.StatusCode, b)
	}
	return out, nil
}
func httpFailure(status int, b []byte) error {
	code, retry := "receiver_error", "never"
	switch status {
	case 400:
		code = "invalid_packet"
	case 401:
		code = "authentication_required"
	case 403:
		code = "forbidden"
	case 404:
		code = "unavailable"
	case 409:
		code, retry = "conflict", "after_reconcile"
	case 410:
		code = "expired"
	case 412:
		code, retry = "revision_changed", "after_reconcile"
	case 413:
		code = "too_large"
	case 415:
		code = "unsupported_media"
	case 429:
		code, retry = "rate_limited", "same_request"
	case 502, 503, 504:
		code, retry = "temporary", "same_request"
	}
	if status >= 300 && status < 400 {
		code = "redirect_refused"
	}
	// Never echo receiver-supplied prose, headers or identifiers from a diagnostic.
	var env struct{ Error struct{ Code string } }
	if json.Unmarshal(b, &env) == nil && slices.Contains([]string{"released", "needs_reconciliation", "request_identity_conflict", "expired", "revision_changed", "unsupported_capability", "unavailable", "temporary", "rate_limited"}, env.Error.Code) {
		code = env.Error.Code
	}
	return &Error{Code: code, HTTP: status, Retry: retry}
}

func trustedLink(base *url.URL, raw string, allowQuery bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host != "" && !u.IsAbs() || (!u.IsAbs() && !strings.HasPrefix(u.Path, "/")) || (!allowQuery && (u.RawQuery != "" || u.ForceQuery)) {
		return nil, failure("invalid_receiver_link", "never")
	}
	u = base.ResolveReference(u)
	if u.Scheme != base.Scheme || u.Host != base.Host || u.Path != path.Clean(u.Path) || strings.Contains(u.Path, "\\") || u.RawPath != "" || !strings.HasPrefix(u.Path, base.Path+"/") {
		return nil, failure("invalid_receiver_link", "never")
	}
	return u, nil
}

// Resolve response references against the request URI. A reverse proxy may
// strip the external mount prefix before forwarding the request to the receiver.
func (c *Client) responseLink(suffix, raw string) (*url.URL, error) {
	ref, err := url.Parse(raw)
	if err != nil || raw == "" || ref.RawPath != "" || ref.Opaque != "" || ref.User != nil || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || strings.Contains(raw, "#") {
		return nil, failure("invalid_receiver_link", "never")
	}
	location := *c.base
	location.Path += suffix
	return trustedLink(c.base, location.ResolveReference(ref).String(), false)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func asHTTP(err error, code int) bool { var e *Error; return errors.As(err, &e) && e.HTTP == code }
