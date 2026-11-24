// Package openclawcli calls the Gateway through a user's existing OpenClaw remote profile.
package openclawcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const responseLimit = 4 << 20

type runner func(context.Context, []string) (json.RawMessage, error)

type Client struct {
	expectedURL string
	run         runner
}

// New checks the transport and the complete receiver base path before using the profile.
func New(ctx context.Context, profileDir, receiverEndpoint, executable string) (*Client, error) {
	if profileDir == "" {
		return nil, errors.New("--profile-dir must name your existing authenticated OpenClaw remote profile")
	}
	dir, err := filepath.Abs(profileDir)
	if err != nil {
		return nil, errors.New("invalid OpenClaw profile directory")
	}
	config := filepath.Join(dir, "openclaw.json")
	if info, err := os.Stat(config); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("remote profile is missing; configure your own normal Gateway access first")
	}
	if executable == "" {
		executable = "openclaw"
	}
	exe, prefix, err := resolveExecutable(executable)
	if err != nil {
		return nil, err
	}
	env := profileEnvironment(os.Environ(), dir, config)
	return newClient(ctx, receiverEndpoint, func(ctx context.Context, args []string) (json.RawMessage, error) {
		ctx, cancel := context.WithTimeout(ctx, 130*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, append(append([]string(nil), prefix...), args...)...)
		cmd.Env = env
		var stdout cappedOutput
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil || !json.Valid(stdout.Bytes()) {
			return nil, errors.New("OpenClaw request failed; check this remote profile's normal authentication, permissions and Gateway availability")
		}
		return json.RawMessage(stdout.Bytes()), nil
	})
}

func resolveExecutable(name string) (string, []string, error) {
	exe, err := exec.LookPath(name)
	if err != nil {
		return "", nil, errors.New("OpenClaw executable unavailable; install the CLI or set --cli")
	}
	// npm's Windows launcher is a batch file. Run its installed JavaScript entry
	// directly so user text and Gateway JSON remain ordinary argv values.
	ext := strings.ToLower(filepath.Ext(exe))
	if runtime.GOOS == "windows" && (ext == ".cmd" || ext == ".bat") {
		root := filepath.Dir(exe)
		entry := filepath.Join(root, "node_modules", "openclaw", "openclaw.mjs")
		if info, err := os.Stat(entry); err != nil || !info.Mode().IsRegular() {
			return "", nil, errors.New("OpenClaw npm launcher has no installed CLI entry; select the OpenClaw executable with --cli")
		}
		node := filepath.Join(root, "node.exe")
		if _, err := os.Stat(node); err != nil {
			node, err = exec.LookPath("node")
			if err != nil {
				return "", nil, errors.New("Node is required for the installed OpenClaw CLI")
			}
		}
		return node, []string{entry}, nil
	}
	return exe, nil, nil
}

func newClient(ctx context.Context, endpoint string, run runner) (*Client, error) {
	readString := func(key string) (string, error) {
		raw, err := run(ctx, []string{"config", "get", key, "--json"})
		if err != nil {
			return "", err
		}
		var val string
		if err = json.Unmarshal(raw, &val); err != nil {
			return "", errors.New("invalid OpenClaw remote profile configuration")
		}
		return val, nil
	}
	mode, err := readString("gateway.mode")
	if err != nil {
		return nil, err
	}
	if mode != "remote" {
		return nil, errors.New("profile must use normal Gateway remote mode")
	}
	remote, err := readString("gateway.remote.url")
	if err != nil {
		return nil, err
	}
	remote = strings.TrimSpace(remote)
	ws, err := url.Parse(remote)
	if err != nil || ws.Hostname() == "" || ws.User != nil || ws.RawQuery != "" || ws.ForceQuery || ws.Fragment != "" {
		return nil, errors.New("profile must configure a remote Gateway WebSocket URL")
	}
	switch strings.ToLower(ws.Scheme) {
	case "ws":
		ws.Scheme = "http"
	case "wss":
		ws.Scheme = "https"
	default:
		return nil, errors.New("profile must configure a remote Gateway WebSocket URL")
	}
	recv, err := url.Parse(endpoint)
	if err != nil || recv.User != nil || recv.Fragment != "" || recv.RawQuery != "" || recv.ForceQuery || recv.Hostname() == "" {
		return nil, errors.New("invalid Beam receiver URL")
	}
	const suffix = "/api/v1/beam/sessions"
	if !strings.HasSuffix(recv.EscapedPath(), suffix) || origin(ws) != origin(recv) || strings.TrimSuffix(ws.EscapedPath(), "/") != strings.TrimSuffix(recv.EscapedPath(), suffix) {
		return nil, errors.New("remote profile targets a different Gateway; select the profile for this Beam endpoint")
	}
	return &Client{expectedURL: remote, run: run}, nil
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else if u.Scheme == "http" {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func profileEnvironment(environ []string, dir, config string) []string {
	var out []string
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "OPENCLAW_GATEWAY_URL", "OPENCLAW_GATEWAY_PORT", "OPENCLAW_GATEWAY_TOKEN", "OPENCLAW_GATEWAY_PASSWORD", "OPENCLAW_STATE_DIR", "OPENCLAW_CONFIG_PATH":
		default:
			out = append(out, entry)
		}
	}
	return append(out, "OPENCLAW_STATE_DIR="+dir, "OPENCLAW_CONFIG_PATH="+config)
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "beam.owned.list", "beam.owned.read", "beam.owned.resolve", "beam.owned.continue",
		"sessions.catalog.list", "sessions.catalog.read", "sessions.catalog.resolve", "sessions.catalog.continue",
		"chat.history", "chat.send":
	default:
		return nil, fmt.Errorf("unsupported OpenClaw method %q", method)
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return c.run(ctx, []string{"gateway", "call", method, "--expect-url", c.expectedURL, "--json", "--timeout", "120000", "--params", string(body)})
}

type cappedOutput struct{ buf bytes.Buffer }

func (b *cappedOutput) Bytes() []byte { return b.buf.Bytes() }
func (b *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > responseLimit-b.buf.Len() {
		return 0, errors.New("OpenClaw response exceeds limit")
	}
	return b.buf.Write(p)
}
