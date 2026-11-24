package localhistory

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
)

// Probe only the binary's public interface. A version string alone cannot prove it supports the receiving switch.
func Capabilities(ctx context.Context, name string) (adapter.Capabilities, error) {
	caps := adapter.Capabilities{Adapter: name, Protocol: 1, Version: "unavailable", Source: []string{"list", "read"}, Destination: []string{}, Recovery: "manual"}
	flags := map[string]string{"claude": "--plugin-dir", "pi": "--extension", "gemini": "--session-file"}
	flag := flags[name]
	bin, err := exec.LookPath(name)
	if err != nil || flag == "" {
		return caps, nil
	}
	home, err := os.MkdirTemp("", "beam-native-probe-")
	if err != nil {
		return caps, err
	}
	defer os.RemoveAll(home)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Version/help must not run workspace integrations or load the user's authenticated settings.
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home, "PATH=" + os.Getenv("PATH"), "NO_COLOR=1", "CI=true", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PI_OFFLINE=1", "GEMINI_CLI_NO_RELAUNCH=true", "GEMINI_CLI_HOME=" + home}
	for _, key := range []string{"SystemRoot", "ComSpec", "PATHEXT", "TMPDIR", "TMP", "TEMP"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	probe := func(arg string, limit int) probeResult {
		cmd := exec.CommandContext(ctx, bin, arg)
		cmd.Env = env
		cmd.Dir = home
		cmd.WaitDelay = 250 * time.Millisecond
		out := limitedOutput{limit: limit}
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return probeResult{err: adapter.Fail("timeout", "same_request", name+" capability probe interrupted before "+arg+" returned")}
			}
			return probeResult{err: adapter.Fail("unavailable", "same_request", name+" could not complete its "+arg+" capability probe")}
		}
		return probeResult{text: strings.TrimSpace(out.buf.String())}
	}
	help := make(chan probeResult, 1)
	go func() { help <- probe("--help", 128<<10) }()
	version := probe("--version", 1024)
	return qualify(caps, flag, version, <-help)
}

type probeResult struct {
	text string
	err  error
}

func qualify(caps adapter.Capabilities, flag string, version, usage probeResult) (adapter.Capabilities, error) {
	if version.err != nil {
		return caps, version.err
	}
	if usage.err != nil {
		return caps, usage.err
	}
	if version.text == "" || strings.ContainsAny(version.text, "\r\n\x1b") {
		return caps, adapter.Fail("unsupported_version", "never", "native version information was malformed")
	}
	caps.Version = version.text
	if strings.Contains(usage.text, flag) {
		caps.Destination = []string{"stageReference", "resolve"}
		caps.Recovery = "lookupByOperationId"
	}
	return caps, nil
}

type limitedOutput struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errors.New("native probe output exceeds limit")
	}
	return w.buf.Write(p)
}
