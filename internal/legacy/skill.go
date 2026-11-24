// Package legacy handles transport for installed v1 Beam skill invocations.
// The skill selects and projects the historical transcript before this boundary.
package legacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/openclaw/beam/internal/openclawcli"
	"github.com/openclaw/beam/internal/packet"
	"github.com/openclaw/beam/internal/receiver"
)

const maxRequestBytes = 512 << 10

type request struct {
	Operation        string `json:"operation"`
	Endpoint         string `json:"endpoint"`
	State            string `json:"state,omitempty"`
	BeamID           string `json:"beamId"`
	Packet           string `json:"packet,omitempty"`
	SnapshotFallback bool   `json:"snapshotFallback,omitempty"`
	Hook             bool   `json:"hook,omitempty"`
	TimeoutMS        int    `json:"timeoutMs,omitempty"`
	ProfileDir       string `json:"profileDir,omitempty"`
	CLI              string `json:"cli,omitempty"`
	Agent            string `json:"agent,omitempty"`
	Message          string `json:"message,omitempty"`
	RequestID        string `json:"requestId,omitempty"`
}

var (
	beamID     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	requestID  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
	sessionKey = regexp.MustCompile(`(?i)^agent:[a-z0-9_-]+:dashboard:[a-z0-9-]+$`)
)

// Run implements the process ABI used by the v1 skill. A receipt is printed only
// after validation and durable storage. After dispatch a caller must never fall
// back to another writer when the result is uncertain.
func Run(ctx context.Context, args []string, in io.Reader, out, diag io.Writer) int {
	if len(args) == 1 && args[0] == "--protocol" {
		fmt.Fprintln(out, "1")
		return 0
	}
	if len(args) != 0 {
		fmt.Fprintln(diag, "usage: beam skill-v1 [--protocol]")
		return 2
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxRequestBytes+1))
	var req request
	if err != nil || len(raw) > maxRequestBytes || packet.DecodeClosed(raw, maxRequestBytes, &req) != nil {
		fmt.Fprintln(diag, "invalid Beam skill request")
		return 1
	}
	result, err := execute(ctx, req)
	if err != nil {
		fmt.Fprintln(diag, displayError(err))
		return 1
	}
	if err = json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(diag, "could not write the Beam receipt")
		return 1
	}
	return 0
}

func execute(ctx context.Context, req request) (any, error) {
	if !beamID.MatchString(req.BeamID) {
		return nil, errors.New("--beam-id must be a 32-character lowercase hex ID")
	}
	switch req.Operation {
	case "share", "handoff", "read", "continue":
	default:
		return nil, errors.New("unsupported Beam skill operation")
	}
	if req.Operation == "share" && len(req.Packet) == 0 || req.Operation != "handoff" && req.SnapshotFallback || req.Hook && req.Operation != "share" {
		return nil, errors.New("invalid Beam skill operation options")
	}
	if len(req.Packet) > 0 {
		p, err := packet.ImportV1([]byte(req.Packet))
		if err != nil || p.BeamID != req.BeamID {
			return nil, errors.New("invalid Beam skill packet")
		}
		if req.Operation != "share" && req.Operation != "handoff" {
			return nil, errors.New("read and continue do not accept a source packet")
		}
	}
	if req.Operation == "continue" {
		if strings.TrimSpace(req.Message) == "" || len(utf16.Encode([]rune(req.Message))) > 32000 {
			return nil, errors.New("--message must contain 1-32000 characters")
		}
		if !requestID.MatchString(req.RequestID) {
			return nil, errors.New("--request-id must contain 1-128 letters, digits, underscores or hyphens")
		}
	}
	endpoint, err := url.Parse(req.Endpoint)
	if err != nil || endpoint.Hostname() == "" {
		return nil, errors.New("invalid Beam endpoint")
	}
	var headers = http.Header{}
	// Legacy skill callers supplied one credential: Access always took precedence
	// over bearer. Retain that authority choice for an existing installed hook.
	if tok := strings.TrimSpace(os.Getenv("BEAM_ACCESS_TOKEN")); tok != "" {
		headers.Set("Cf-Access-Token", tok)
	} else if tok := strings.TrimSpace(os.Getenv("BEAM_AUTH_TOKEN")); tok != "" {
		headers.Set("Authorization", "Bearer "+tok)
	}
	local := endpoint.Hostname() == "localhost" || net.ParseIP(endpoint.Hostname()).IsLoopback()
	cloudflare := (req.Operation == "share" || req.Operation == "handoff") && !local && !req.Hook && len(headers) == 0
	uncredentialedHook := req.Hook && !local && len(headers) == 0
	timeout := 15 * time.Second
	if req.TimeoutMS != 0 {
		if req.TimeoutMS < 1 || req.TimeoutMS > 2147483647 {
			return nil, errors.New("invalid Beam request timeout")
		}
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	httpClient := &http.Client{Timeout: timeout}
	if uncredentialedHook {
		httpClient.Transport = noHookNetwork{}
	}
	client, err := receiver.New(receiver.Config{Endpoint: req.Endpoint, StatePath: req.State, Headers: headers, CloudflareLogin: cloudflare, HTTPClient: httpClient})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	switch req.Operation {
	case "share":
		receipt, err := client.ShareV1(ctx, []byte(req.Packet))
		var remote *receiver.Error
		if uncredentialedHook && errors.As(err, &remote) && remote.Code == "transport_failed" {
			return nil, errors.New("no Beam credentials configured for non-interactive hook publishing")
		}
		return receipt, err
	case "handoff":
		return client.HandoffV1(ctx, req.BeamID, []byte(req.Packet), req.SnapshotFallback)
	default:
		return destination(ctx, client, req)
	}
}

func destination(ctx context.Context, c *receiver.Client, req request) (any, error) {
	row, exists, err := c.LegacyRow(ctx, req.BeamID)
	if err != nil {
		return nil, err
	}
	var saved struct {
		BeamID    string             `json:"beamId"`
		Handoff   receiver.V1Handoff `json:"handoff"`
		SessionID string             `json:"sessionId"`
	}
	if !exists || json.Unmarshal(row.Receipt, &saved) != nil || saved.BeamID != req.BeamID || saved.Handoff.Owner != "gateway" || !beamID.MatchString(saved.Handoff.ID) || (saved.Handoff.Access != "owned" && saved.Handoff.Access != "shared") {
		return nil, errors.New("run handoff for this source and endpoint first; its ownership transfer is not yet acknowledged")
	}
	gateway, err := openclawcli.New(ctx, req.ProfileDir, req.Endpoint, req.CLI)
	if err != nil {
		return nil, err
	}
	prefix := "sessions.catalog"
	if saved.Handoff.Access == "owned" {
		prefix = "beam.owned"
	}
	locator := map[string]string{"catalogId": "beam", "hostId": "gateway", "threadId": req.BeamID}
	if req.Agent != "" {
		locator["agentId"] = req.Agent
	}
	binding := func(method string) (string, error) {
		raw, err := gateway.Call(ctx, prefix+"."+method, locator)
		if err != nil {
			return "", err
		}
		var result struct {
			Key string `json:"sessionKey"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return "", errors.New("invalid OpenClaw destination receipt")
		}
		return result.Key, nil
	}
	key, err := binding("resolve")
	if err != nil {
		return nil, err
	}
	if key == "" && row.SessionKey == "" {
		if req.Operation == "read" {
			return map[string]string{"status": "pending", "beamId": req.BeamID, "message": "Source released. No destination turn exists yet; use beam continue with your normal remote profile to start it."}, nil
		}
		key, err = binding("continue")
		if err != nil {
			return nil, err
		}
	}
	if !sessionKey.MatchString(key) || (row.SessionKey != "" && key != row.SessionKey) {
		return nil, errors.New("Gateway did not return the original authoritative conversation; reopen its Beam link")
	}
	raw, err := gateway.Call(ctx, "chat.history", map[string]any{"sessionKey": key, "limit": 100})
	if err != nil {
		return nil, err
	}
	var history struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &history) != nil || history.SessionID == "" || (saved.SessionID != "" && history.SessionID != saved.SessionID) {
		return nil, errors.New("the destination was deleted or reset; reconcile the original conversation before continuing")
	}
	if err = c.SaveLegacyDestination(ctx, req.BeamID, key, history.SessionID); err != nil {
		return nil, err
	}
	if req.Operation == "read" {
		return raw, nil
	}
	return gateway.Call(ctx, "chat.send", map[string]string{"sessionKey": key, "sessionId": history.SessionID, "message": req.Message, "idempotencyKey": req.RequestID})
}

func displayError(err error) string {
	var e *receiver.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_receiver_link", "invalid_link":
			return "Beam receiver returned an unsafe session URL"
		case "invalid_can_view":
			return "Beam receiver returned an invalid canView receipt field"
		case "invalid_handoff_receipt":
			return "Beam receiver returned an invalid handoff receipt"
		case "https_required":
			return "Beam endpoints must use HTTPS unless they are loopback-local"
		}
		if e.HTTP != 0 {
			return fmt.Sprintf("Beam upload failed: HTTP %d", e.HTTP)
		}
	}
	// An OS or parse error may carry paths or the native request. Keep their
	// details local; only emit the stable error classes returned above.
	if strings.ContainsAny(err.Error(), "\r\n") || bytes.ContainsAny([]byte(err.Error()), "/\\@") {
		return "Beam operation failed; check the endpoint, state file and selected remote profile"
	}
	return err.Error()
}

// An already retired source can return its saved receipt without credentials.
// Refuse any network use at the moment a fresh unattended upload would start.
type noHookNetwork struct{}

func (noHookNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("non-interactive hook requires credentials")
}
