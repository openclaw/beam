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
func Capabilities(ctx context.Context, name string) adapter.Capabilities {
	caps := adapter.Capabilities{Adapter: name, Protocol: 1, Version: "unavailable", Source: []string{"list", "read"}, Destination: []string{}, Recovery: "manual"}
	flags := map[string]string{"claude": "--plugin-dir", "pi": "--extension", "gemini": "--session-file"}
	flag := flags[name]
	bin, err := exec.LookPath(name)
	if err != nil || flag == "" {
		return caps
	}
	home, err := os.MkdirTemp("", "beam-native-probe-")
	if err != nil {
		return caps
	}
	defer os.RemoveAll(home)
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	// Version/help must not run workspace integrations or load the user's authenticated settings.
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home, "PATH=" + os.Getenv("PATH"), "NO_COLOR=1", "CI=true", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PI_OFFLINE=1", "GEMINI_CLI_NO_RELAUNCH=true", "GEMINI_CLI_HOME=" + home}
	for _, key := range []string{"SystemRoot", "ComSpec", "PATHEXT", "TMPDIR", "TMP", "TEMP"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	probe := func(arg string, limit int) string {
		cmd := exec.CommandContext(ctx, bin, arg)
		cmd.Env = env
		cmd.Dir = home
		cmd.WaitDelay = 250 * time.Millisecond
		out := limitedOutput{limit: limit}
		cmd.Stdout = &out
		if cmd.Run() != nil {
			return ""
		}
		return strings.TrimSpace(out.buf.String())
	}
	// Run both read-only queries against the same isolated configuration within doctor's deadline.
	help := make(chan string, 1)
	go func() { help <- probe("--help", 128<<10) }()
	version := probe("--version", 1024)
	usage := <-help
	if version == "" || strings.ContainsAny(version, "\r\n\x1b") {
		return caps
	}
	caps.Version = version
	if strings.Contains(usage, flag) {
		caps.Destination = []string{"stageReference", "resolve"}
		caps.Recovery = "lookupByOperationId"
	}
	return caps
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
