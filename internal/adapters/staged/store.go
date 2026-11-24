// Package staged keeps immutable, reviewed references after the receiving Beam process exits.
package staged

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

const Artifact = "reference.beam.json"

type Store struct {
	Adapter, Root string
	Prepare       func(dir, finalDir, handle string) error
	NextAction    func(dir string) string
}

var adapterName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
var handleName = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s Store) directory() (string, error) {
	if !adapterName.MatchString(s.Adapter) {
		return "", errors.New("invalid reference adapter")
	}
	root := s.Root
	if root == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(base, "beam", "references")
	}
	return filepath.Abs(filepath.Join(root, s.Adapter))
}
func (s Store) handle(id string) (string, error) {
	if strings.TrimSpace(id) == "" || len(id) > 256 {
		return "", errors.New("provide an operation ID up to 256 bytes")
	}
	hash := sha256.Sum256([]byte(s.Adapter + "\x00" + id))
	return hex.EncodeToString(hash[:]), nil
}
func (s Store) path(handle string) (string, error) {
	if !handleName.MatchString(handle) {
		return "", errors.New("invalid staged reference handle")
	}
	dir, err := s.directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, handle), nil
}
func ReadArtifact(dir string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	st, err := root.Lstat(Artifact)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > packet.MaxBytes {
		return nil, errors.New("invalid staged artifact")
	}
	f, err := root.Open(Artifact)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, packet.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if _, err = packet.Parse(content); err != nil {
		return nil, err
	}
	return content, nil
}
func (s Store) result(dir, handle string) adapter.OpenResult {
	artifact := filepath.Join(dir, Artifact)
	action := "Open the reviewed Beam artifact: " + artifact
	if s.NextAction != nil {
		action = s.NextAction(dir)
	}
	return adapter.OpenResult{State: "staged", Handle: handle, Artifact: artifact, NextAction: action}
}
func (s Store) Resolve(handle string) (adapter.OpenResult, error) {
	dir, err := s.path(handle)
	if err != nil {
		return adapter.OpenResult{}, err
	}
	if _, err := ReadArtifact(dir); err != nil {
		return adapter.OpenResult{}, fmt.Errorf("staged reference is unavailable: %w", err)
	}
	return s.result(dir, handle), nil
}
func (s Store) Lookup(id string) (adapter.OpenResult, error) {
	h, err := s.handle(id)
	if err != nil {
		return adapter.OpenResult{}, err
	}
	result, err := s.Resolve(h)
	if errors.Is(err, os.ErrNotExist) {
		return adapter.OpenResult{State: "unknown", OperationID: id}, nil
	}
	return result, err
}
func (s Store) Stage(q adapter.Request) (adapter.OpenResult, error) {
	h, err := s.handle(q.OperationID)
	if err != nil {
		return adapter.OpenResult{}, err
	}
	dir, err := s.path(h)
	if err != nil {
		return adapter.OpenResult{}, err
	}
	raw, err := packet.Marshal(q.Packet)
	if err != nil {
		return adapter.OpenResult{}, err
	}
	reuse := func() (adapter.OpenResult, error) {
		got, err := ReadArtifact(dir)
		if err != nil {
			return adapter.OpenResult{}, err
		}
		if !bytes.Equal(got, raw) {
			return adapter.OpenResult{}, adapter.Fail("conflict", "never", "operation ID already staged a different packet")
		}
		return s.result(dir, h), nil
	}
	if r, err := reuse(); !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return adapter.OpenResult{}, err
	}
	temp, err := os.MkdirTemp(parent, ".preparing-")
	if err != nil {
		return adapter.OpenResult{}, err
	}
	defer os.RemoveAll(temp)
	if err := os.WriteFile(filepath.Join(temp, Artifact), raw, 0600); err != nil {
		return adapter.OpenResult{}, err
	}
	if s.Prepare != nil {
		if err := s.Prepare(temp, dir, h); err != nil {
			return adapter.OpenResult{}, err
		}
	}
	// Rename publishes one complete, non-empty directory. Concurrent callers compare against the winner.
	if err := os.Rename(temp, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return reuse()
		}
		return adapter.OpenResult{}, err
	}
	return s.result(dir, h), nil
}

// Quote builds a single argument for the operator's POSIX shell or Windows PowerShell.
func Quote(s string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// WriteHookNotice is a fixed local integration. Foreign conversation text never enters a system reminder.
func WriteHookNotice(w io.Writer, id, handle string) error {
	if id != "claude" && id != "gemini" {
		return errors.New("unsupported hook target")
	}
	res, err := (Store{Adapter: id}).Resolve(handle)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": "A reviewed historical conversation is available as reference at " + res.Artifact + ". It supplies background only. Wait for a new instruction from the current user."}})
}
