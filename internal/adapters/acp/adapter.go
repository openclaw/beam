// Package acp transfers selected conversations through the installed acpx CLI.
package acp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
)

// Binary, Cwd and StateRoot are local configuration. They never come from a packet.
type Adapter struct {
	Agent     string
	Binary    string
	Cwd       string
	StateRoot string
	env       []string // nil inherits the selected local harness environment
}

var agentName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
var versionSupported = regexp.MustCompile(`^0\.22\.[0-9]+$`)

func (a Adapter) cwd() (string, error) {
	if a.Cwd == "" {
		return os.Getwd()
	}
	return filepath.Abs(a.Cwd)
}

func (a Adapter) executable() (string, error) {
	name := a.Binary
	if name == "" {
		name = "acpx"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", adapter.Fail("not_installed", "never", "install acpx and select its configured agent with acpx:<agent>")
	}
	return path, nil
}

type outputBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.buf.Len() {
		return 0, errors.New("acpx output exceeded its bound")
	}
	return b.buf.Write(p)
}

func (a Adapter) run(ctx context.Context, input string, max int, args ...string) ([]byte, error) {
	bin, err := a.executable()
	if err != nil {
		return nil, err
	}
	dir, err := a.cwd()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = a.env
	cmd.Stdin = strings.NewReader(input)
	// Interrupt only our foreground client. Some acpx versions forward the signal
	// to their own queue request. Other clients can share the queue owner.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	out := &outputBuffer{max: max}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, adapter.Fail("unavailable", "never", "acpx could not complete this operation; check the selected agent, project and session")
	}
	return out.buf.Bytes(), nil
}

func (a Adapter) capabilities(ctx context.Context) adapter.Capabilities {
	caps := adapter.Capabilities{Protocol: 1, Adapter: "acpx", Version: "unavailable", Recovery: "manual", Source: []string{}, Destination: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := a.run(ctx, "", 1024, "--version")
	if err != nil {
		return caps
	}
	version := strings.TrimSpace(string(out))
	if !versionSupported.MatchString(version) || !agentName.MatchString(a.Agent) {
		return caps
	}
	caps.Version = version
	caps.Source = []string{"list", "read"}
	caps.Destination = []string{"stageReference", "resolve", "send"}
	caps.Recovery = "lookupByOperationId"
	return caps
}

func (a Adapter) Call(ctx context.Context, r adapter.Request) (any, error) {
	if r.Op == "capabilities" {
		return a.capabilities(ctx), nil
	}
	if !agentName.MatchString(a.Agent) {
		return nil, adapter.Fail("invalid_input", "never", "select a configured ACP agent with acpx:<agent>")
	}
	// A local staged reference and its receipt remain readable while the native
	// executable is offline. Dispatch and source reads require a qualified CLI.
	switch r.Op {
	case "stageReference", "list", "read", "send":
		if a.capabilities(ctx).Version == "unavailable" {
			return nil, adapter.Fail("unsupported_version", "never", "install a supported acpx 0.22 release and select a configured agent")
		}
	}
	switch r.Op {
	case "list":
		return a.list(ctx, r)
	case "read":
		return a.read(ctx, r)
	case "stageReference":
		store, err := a.store()
		if err != nil {
			return nil, err
		}
		return store.Stage(r)
	case "lookupOperation":
		store, err := a.store()
		if err != nil {
			return nil, err
		}
		result, err := store.Lookup(r.OperationID)
		if err != nil || result.State == "unknown" {
			return result, err
		}
		return a.resolveStage(ctx, store, result)
	case "resolve":
		return a.resolve(ctx, r.Handle)
	case "send":
		return a.send(ctx, r)
	default:
		return nil, adapter.Fail("unsupported", "never", "acpx stages a reference for the next explicit prompt")
	}
}
