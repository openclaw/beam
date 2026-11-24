package acp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/staged"
	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

func (a Adapter) store() (staged.Store, error) {
	dir, err := a.cwd()
	if err != nil {
		return staged.Store{}, err
	}
	digest := sha256.Sum256([]byte(a.Agent + "\x00" + dir))
	return staged.Store{Adapter: "acp-" + hex.EncodeToString(digest[:12]), Root: a.StateRoot,
		NextAction: func(dir string) string {
			return "Use the reviewed reference at " + filepath.Join(dir, staged.Artifact) + " in a new " + a.Agent + " session"
		}}, nil
}

type binding struct {
	Record  string `json:"record"`
	Session string `json:"session"`
}

func readBinding(dir string) (binding, bool, error) {
	var b binding
	data, err := os.ReadFile(filepath.Join(dir, "acpx-binding.json"))
	if errors.Is(err, os.ErrNotExist) {
		return b, false, nil
	}
	if err != nil {
		return b, false, err
	}
	if json.Unmarshal(data, &b) != nil || b.Record == "" || b.Session == "" {
		return b, false, errors.New("invalid acpx binding")
	}
	return b, true, nil
}
func storeBinding(dir string, b binding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".binding-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, "acpx-binding.json"))
}

func uncertain() error {
	return adapter.Fail("needs_reconciliation", "after_reconcile", "a prior acpx operation may have run; inspect the selected session before submitting another request")
}

func (a Adapter) bound(ctx context.Context, handle string, b binding) (nativeRecord, error) {
	rec, err := a.show(ctx, "beam-"+handle)
	if err != nil || rec.ID != b.Record || rec.NativeID != b.Session || rec.Closed {
		return nativeRecord{}, uncertain()
	}
	return rec, nil
}

func (a Adapter) resolve(ctx context.Context, handle string) (adapter.OpenResult, error) {
	store, err := a.store()
	if err != nil {
		return adapter.OpenResult{}, err
	}
	result, err := store.Resolve(handle)
	if err != nil {
		return adapter.OpenResult{}, err
	}
	return a.resolveStage(ctx, store, result)
}

func (a Adapter) resolveStage(ctx context.Context, store staged.Store, result adapter.OpenResult) (adapter.OpenResult, error) {
	handle := result.Handle
	path, err := journal.DefaultPath()
	if err != nil {
		return adapter.OpenResult{}, err
	}
	// Passive recovery must see an interrupted first send without creating
	// a journal merely to look up a staged reference.
	if _, err := os.Stat(path); err == nil {
		j, err := journal.Open(path)
		if err != nil {
			return adapter.OpenResult{}, err
		}
		defer j.Close()
		_, pending, err := j.Pending(ctx, "acpx-local:"+store.Adapter, "acpx-prompt", handle)
		if err != nil {
			return adapter.OpenResult{}, err
		}
		if pending {
			result.State = "needs_reconciliation"
			result.NextAction = "Inspect the acpx session beam-" + handle + " before submitting another request."
			return result, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return adapter.OpenResult{}, err
	}
	b, found, err := readBinding(filepath.Dir(result.Artifact))
	if err != nil {
		return adapter.OpenResult{}, err
	}
	if !found {
		return result, nil
	}
	if _, err = a.bound(ctx, handle, b); err != nil {
		return adapter.OpenResult{State: "needs_reconciliation", Handle: handle, NextAction: "Inspect the bound acpx session."}, nil
	}
	return adapter.OpenResult{State: "ready", Handle: handle, NativeSession: b.Record, NextAction: "Open the named acpx session beam-" + handle}, nil
}

// A clean terminal result must belong to this session/prompt request. Tool or
// agent-generated content cannot substitute for the terminal JSON-RPC response.
func promptCompleted(raw []byte, session string) bool {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), adapter.MaxResponseBytes)
	var id string
	for scanner.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
			Params struct {
				SessionID string `json:"sessionId"`
			}
			Result struct {
				StopReason string `json:"stopReason"`
			}
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "session/prompt" && m.Params.SessionID == session {
			id = string(m.ID)
			continue
		}
		if id != "" && m.Method == "" && string(m.ID) == id && m.Result.StopReason == "end_turn" {
			return true
		}
	}
	return false
}

func (a Adapter) send(ctx context.Context, r adapter.Request) (adapter.SendResult, error) {
	result := adapter.SendResult{Handle: r.Handle, RequestID: r.RequestID, State: "completed"}
	if strings.TrimSpace(r.Instruction) == "" || len(r.Instruction) > 64*1024 || strings.TrimSpace(r.RequestID) == "" || len(r.RequestID) > 256 {
		return result, adapter.Fail("invalid_input", "never", "supply a bounded fresh instruction and request ID")
	}
	store, err := a.store()
	if err != nil {
		return result, err
	}
	stage, err := store.Resolve(r.Handle)
	if err != nil {
		return result, err
	}
	dir := filepath.Dir(stage.Artifact)
	j, err := journal.Open("")
	if err != nil {
		return result, err
	}
	defer j.Close()
	endpoint := "acpx-local:" + store.Adapter
	digest := packet.Digest([]byte(r.Instruction))
	sent := func() (bool, error) {
		resource, body, ok, err := j.Sent(ctx, endpoint, r.RequestID)
		if err != nil {
			return false, err
		}
		if ok && (resource != r.Handle || body != digest) {
			return false, adapter.Fail("request_identity_conflict", "never", "this request ID was used with different input")
		}
		return ok, nil
	}
	if ok, err := sent(); ok || err != nil {
		return result, err
	}
	// Bind and dispatch under a single persistent first-writer claim. ACPX
	// allocates its own request id, so a lost acknowledgement cannot be replayed.
	intent := journal.Pending{Endpoint: endpoint, Kind: "acpx-prompt", Resource: r.Handle, ID: r.RequestID, Condition: rand.Text(), Body: []byte(digest), Created: time.Now()}
	winner, err := j.Begin(ctx, intent)
	if err != nil {
		return result, err
	}
	if winner.Condition != intent.Condition {
		return result, uncertain()
	}
	if ok, err := sent(); ok || err != nil {
		if ok {
			err = j.Clear(ctx, intent)
		}
		return result, err
	}
	b, existing, err := readBinding(dir)
	if err != nil {
		return result, err
	}
	message := r.Instruction
	name := "beam-" + r.Handle
	if existing {
		if _, err = a.bound(ctx, r.Handle, b); err != nil {
			return result, err
		}
	} else {
		raw, err := staged.ReadArtifact(dir)
		if err != nil {
			return result, err
		}
		reviewed, err := packet.Parse(raw)
		if err != nil {
			return result, err
		}
		message = "The following quoted transcript is historical reference material.\n\n" + packet.Render(reviewed) + "\n\nCurrent user instruction:\n" + r.Instruction
		raw, err = a.run(ctx, "", 65536, "--format", "json", a.Agent, "sessions", "ensure", "--name", name)
		if err != nil {
			return result, uncertain()
		}
		var response struct {
			Created bool
			Name    string
			Record  string `json:"acpxRecordId"`
			Session string `json:"acpxSessionId"`
		}
		if json.Unmarshal(raw, &response) != nil || !response.Created || response.Record == "" || response.Session == "" || response.Name != name {
			return result, uncertain()
		}
		b = binding{response.Record, response.Session}
	}
	raw, err := a.run(ctx, message, adapter.MaxResponseBytes, "--prompt-retries", "0", "--format", "json", "--json-strict", a.Agent, "prompt", "-s", name, "--expect-record", b.Record, "--file", "-")
	if err != nil || !promptCompleted(raw, b.Session) {
		return result, uncertain()
	}
	if _, err = a.bound(ctx, r.Handle, b); err != nil {
		return result, uncertain()
	}
	if !existing {
		if err = storeBinding(dir, b); err != nil {
			return result, fmt.Errorf("save acpx binding: %w", err)
		}
	}
	if err = j.ConfirmSend(ctx, intent, digest); err != nil {
		return result, uncertain()
	}
	return result, nil
}
