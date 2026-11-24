package openclawcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NewSource uses normal Gateway authentication. Explicit remote profiles must match the
// exact expected origin and base path before any history is read.
func NewSource(ctx context.Context, profileDir, endpoint, executable string) (*Client, string, error) {
	if profileDir != "" || endpoint != "" {
		if endpoint == "" || profileDir == "" {
			return nil, "", errors.New("OpenClaw remote export requires both --profile-dir and --endpoint")
		}
		legacy := strings.TrimRight(endpoint, "/")
		if !strings.HasSuffix(legacy, "/api/v1/beam/sessions") {
			legacy += "/api/v1/beam/sessions"
		}
		c, err := New(ctx, profileDir, legacy, executable)
		if err != nil {
			return nil, "", err
		}
		return c, c.expectedURL, nil
	}
	dir := os.Getenv("OPENCLAW_STATE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		dir = filepath.Join(home, ".openclaw")
	}
	config := os.Getenv("OPENCLAW_CONFIG_PATH")
	if config == "" {
		config = filepath.Join(dir, "openclaw.json")
	}
	if executable == "" {
		executable = "openclaw"
	}
	exe, prefix, err := resolveExecutable(executable)
	if err != nil {
		return nil, "", err
	}
	return &Client{run: jsonRunner(exe, prefix, nil, "OpenClaw read failed; check access to this Gateway and selected session")}, config, nil
}

func (c *Client) SourceCall(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "sessions.list", "chat.history", "sessions.catalog.read":
	default:
		return nil, errors.New("unsupported OpenClaw source method")
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	args := []string{"gateway", "call", method}
	if c.expectedURL != "" {
		args = append(args, "--expect-url", c.expectedURL)
	}
	args = append(args, "--json", "--timeout", "120000", "--params", string(b))
	return c.run(ctx, args)
}

func SourceVersion(ctx context.Context, executable string) (string, error) {
	if executable == "" {
		executable = "openclaw"
	}
	exe, prefix, err := resolveExecutable(executable)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, exe, append(append([]string{}, prefix...), "--version")...)
	var out cappedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", errors.New("OpenClaw CLI version probe failed")
	}
	fields := strings.Fields(string(out.Bytes()))
	if len(fields) == 0 {
		return "", errors.New("OpenClaw CLI returned no version")
	}
	return strings.Join(fields, " "), nil
}
