package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

func bindingKey(id string, a adapter.Adapter) string {
	dir, _ := os.Getwd()
	executable := ""
	if x, ok := a.(adapter.External); ok {
		executable = x.Path
	}
	encoded, _ := json.Marshal([]string{id, dir, executable})
	return packet.Digest(encoded)
}

func recordCall[T any](ctx context.Context, target string, a adapter.Adapter, r adapter.Request, recovery string, storePath string) (T, error) {
	var zero T
	db, err := journal.Open(storePath)
	if err != nil {
		return zero, err
	}
	defer db.Close()
	body, err := adapter.EncodeRequest(r)
	if err != nil {
		return zero, err
	}
	id := r.OperationID
	if r.Op == "send" {
		id = r.RequestID
	}
	rec, first, err := db.BeginAdapter(ctx, journal.AdapterOperation{Target: target, ID: id, Op: r.Op, Digest: packet.Digest(body)})
	if errors.Is(err, journal.ErrConflict) {
		return zero, adapter.Fail("request_identity_conflict", "never", "operation ID was used for a different request")
	}
	if err != nil {
		return zero, err
	}
	var value T
	if len(rec.Receipt) > 0 {
		err = json.Unmarshal(rec.Receipt, &value)
		if err != nil {
			return zero, err
		}
		if opened, ok := any(value).(adapter.OpenResult); ok && opened.State == "ready" {
			if err := verifyBinding(ctx, a, opened, id); err != nil {
				return zero, err
			}
		}
		return value, nil
	}
	if !first {
		if recovery != "lookupByOperationId" || r.Op == "send" {
			return zero, uncertain(id)
		}
		recovered, err := call[adapter.OpenResult](ctx, a, adapter.Request{Op: "lookupOperation", OperationID: id})
		if err != nil {
			return zero, err
		}
		if recovered.State != "ready" && recovered.State != "staged" && recovered.State != "manual" {
			return zero, uncertain(id)
		}
		var ok bool
		value, ok = any(recovered).(T)
		if !ok {
			return zero, uncertain(id)
		}
	} else {
		value, err = call[T](ctx, a, r)
		if err != nil {
			// Keep the pre-dispatch intent. A later retry can reconcile even if the child
			// changed native state before returning a transport error.
			typed := adapter.Describe(err)
			return zero, adapter.Fail(typed.Code, typed.Retry, fmt.Sprintf("operation %s: %s", id, typed.Message))
		}
	}
	receipt, err := json.Marshal(value)
	if err != nil {
		return zero, err
	}
	if err = db.FinishAdapter(ctx, rec, receipt); err != nil {
		return zero, uncertain(id)
	}
	return value, nil
}

func verifyBinding(ctx context.Context, dst adapter.Adapter, saved adapter.OpenResult, id string) error {
	current, err := call[adapter.OpenResult](ctx, dst, adapter.Request{Op: "resolve", Handle: saved.Handle})
	if err != nil || current.State != "ready" || current.NativeSession != saved.NativeSession {
		return uncertain(id)
	}
	return nil
}

func uncertain(id string) error {
	return adapter.Fail("needs_reconciliation", "after_reconcile", fmt.Sprintf("operation %s may have completed; use its existing reference to reconcile", id))
}

// lookupLocal uses already-persisted receipts when a harness cannot look up a lost operation.
func lookupLocal(ctx context.Context, target string, dst adapter.Adapter, id, dbPath string) (any, error) {
	if dbPath == "" {
		var err error
		dbPath, err = journal.DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(dbPath); err == nil {
		db, err := journal.Open(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		row, ok, err := db.AdapterOperation(ctx, target, id)
		if err != nil {
			return nil, err
		}
		if ok && len(row.Receipt) > 0 {
			if row.Op == "send" {
				var r adapter.SendResult
				err = json.Unmarshal(row.Receipt, &r)
				return r, err
			}
			var r adapter.OpenResult
			err = json.Unmarshal(row.Receipt, &r)
			if err == nil && r.Handle != "" && r.State == "ready" {
				if err := verifyBinding(ctx, dst, r, id); err != nil {
					return nil, err
				}
			}
			return r, err
		}
		if ok && row.Op == "send" {
			return nil, uncertain(id)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	c, err := caps(ctx, dst)
	if err != nil {
		return nil, err
	}
	if c.Recovery != "lookupByOperationId" {
		return adapter.OpenResult{State: "unknown", OperationID: id}, nil
	}
	return call[adapter.OpenResult](ctx, dst, adapter.Request{Op: "lookupOperation", OperationID: id})
}
