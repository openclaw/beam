package localhistory

import (
	"context"
	"slices"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/staged"
)

func Dispatch(ctx context.Context, q adapter.Request, store staged.Store, reader func() (Reader, error)) (any, error) {
	switch q.Op {
	case "capabilities":
		return Capabilities(ctx, store.Adapter), nil
	case "list", "read":
		r, err := reader()
		if err != nil {
			return nil, err
		}
		return r.Call(ctx, q)
	case "stageReference":
		caps := Capabilities(ctx, store.Adapter)
		if caps.Version == "unavailable" {
			return nil, adapter.Fail("not_installed", "after_setup", "install "+store.Adapter+" before opening a staged reference")
		}
		if !slices.Contains(caps.Destination, q.Op) {
			return nil, adapter.Fail("unsupported_version", "after_setup", "the installed "+store.Adapter+" does not advertise its native reference option")
		}
		return store.Stage(q)
	case "resolve":
		return store.Resolve(q.Handle)
	case "lookupOperation":
		return store.Lookup(q.OperationID)
	default:
		return nil, adapter.Fail("unsupported_operation", "never", store.Adapter+" supports reading and staging a reference; type the first instruction in the harness")
	}
}
