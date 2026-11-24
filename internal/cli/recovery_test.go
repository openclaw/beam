package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
)

type durableFixture struct {
	opens, sends int
	lost         bool
	ref          adapter.OpenResult
}

func (a *durableFixture) Call(_ context.Context, r adapter.Request) (any, error) {
	switch r.Op {
	case "capabilities":
		return adapter.Capabilities{Protocol: 1, Adapter: "fixture", Version: "1", Recovery: "lookupByOperationId", Destination: []string{"openCopy", "resolve", "send"}}, nil
	case "openCopy":
		a.opens++
		a.ref = adapter.OpenResult{State: "ready", NativeSession: "native-1", Handle: "h"}
		if a.lost {
			a.lost = false
			return nil, adapter.Fail("needs_reconciliation", "after_reconcile", "lost response")
		}
		return a.ref, nil
	case "lookupOperation", "resolve":
		return a.ref, nil
	case "send":
		a.sends++
		return adapter.SendResult{State: "accepted", Handle: r.Handle, RequestID: r.RequestID}, nil
	}
	return nil, errors.New("unknown")
}
func TestLocalRetryAndSendIdentities(t *testing.T) {
	fixture := &durableFixture{lost: true}
	a := App{Adapters: Registry{"fixture": fixture}}
	raw := `{"version":2,"beamId":"0123456789abcdef0123456789abcdef","source":"test","title":"Hello","updatedAt":"2026-01-01T00:00:00Z","completed":false,"items":[{"type":"userMessage","text":"hello"}]}`
	args := []string{"open", "-", "--in", "fixture", "--state", filepath.Join(t.TempDir(), "beam.db"), "--operation-id", "op1", "--message", "new instruction", "--json"}
	first, _, _ := invoke(t, a, raw, args...)
	if first != 5 {
		t.Fatal(first)
	}
	for range 2 {
		code, out, _ := invoke(t, a, raw, args...)
		if code != 0 || !json.Valid(out) {
			t.Fatal(code, string(out))
		}
	}
	if fixture.opens != 1 || fixture.sends != 1 {
		t.Errorf("duplicate work: opens=%d sends=%d", fixture.opens, fixture.sends)
	}
	args[len(args)-2] = "changed"
	code, _, _ := invoke(t, a, raw, args...)
	if code != 5 || fixture.sends != 1 {
		t.Fatal(code, fixture.sends)
	}
}
