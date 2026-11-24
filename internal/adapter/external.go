package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"
)

// External runs an explicitly selected local adapter without a shell. Each request starts a new child.
type External struct {
	Path    string
	Timeout time.Duration
}

func (a External) Call(ctx context.Context, r Request) (any, error) {
	if !filepath.IsAbs(a.Path) {
		return nil, fmt.Errorf("external adapter must be an absolute executable path")
	}
	data, err := EncodeRequest(r)
	if err != nil {
		return nil, err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(childCtx, a.Path)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(data)
	out := outputLimit{limit: MaxResponseBytes, cancel: cancel}
	cmd.Stdout = &out
	// Adapter stderr may contain native diagnostics and transcripts. The wire error is the public diagnostic.
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	mutating := r.Op == "openCopy" || r.Op == "stageReference" || r.Op == "send"
	if out.overflow || childCtx.Err() != nil {
		if mutating {
			return nil, Fail("needs_reconciliation", "after_reconcile", "adapter operation may have completed; reconcile the existing operation ID")
		}
		if out.overflow {
			return nil, Fail("too_large", "never", "adapter output exceeds 4 MiB")
		}
		return nil, Fail("timeout", "same_request", "adapter call was canceled or timed out")
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return nil, Fail("unavailable", "never", "cannot launch the configured adapter executable")
	}
	result, err := DecodeResult(out.buf.Bytes(), r.Op)
	if err != nil {
		var reported *Error
		if errors.As(err, &reported) {
			return nil, err
		}
		if mutating {
			return nil, Fail("needs_reconciliation", "after_reconcile", "adapter returned no valid receipt; reconcile the original operation")
		}
		return nil, err
	}
	if runErr != nil {
		return nil, Fail("temporary", "after_reconcile", "adapter reported success but exited unsuccessfully")
	}
	return result, nil
}

type outputLimit struct {
	buf      bytes.Buffer
	limit    int
	cancel   func()
	overflow bool
}

func (w *outputLimit) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.buf.Len() {
		w.overflow = true
		w.cancel()
		return 0, fmt.Errorf("output limit exceeded")
	}
	return w.buf.Write(p)
}
