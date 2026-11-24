package openclawcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListRouteUsesCurrentGatewayCatalogAndOnlyItsAttestedMethod(t *testing.T) {
	const target = "wss://gateway.example/team"
	const route = `{"catalogId":"external-catalog","routeSegment":"beam","methods":{"list":"notebook.mine.list","read":"notebook.mine.read"}}`
	for _, tc := range []struct {
		reply string
		lists bool
	}{{route, true}, {"null", false}, {strings.Replace(route, "notebook.mine.list", "--url", 1), false}, {strings.Replace(route, `"beam"`, `"other"`, 1), false}} {
		count := 0
		c := &Client{expectedURL: target, run: func(_ context.Context, args []string) (json.RawMessage, error) {
			if args[3] != "--expect-url" || args[4] != target {
				t.Fatal("lost pinned remote")
			}
			switch args[2] {
			case "sessions.catalog.route":
				return json.RawMessage(tc.reply), nil
			case "notebook.mine.list":
				count++
				var p struct {
					CatalogID, Search string
					LimitPerHost      int
				}
				if json.Unmarshal([]byte(args[len(args)-1]), &p) != nil || p.CatalogID != "external-catalog" || p.Search != "0123456789ab" || p.LimitPerHost != 2 {
					t.Fatal("lost registered catalog, prefix or limit")
				}
				return json.RawMessage(`{"catalogs":[]}`), nil
			default:
				t.Fatalf("unexpected method %q", args[2])
				return nil, nil
			}
		}}
		id, _, err := c.ListRoute(context.Background(), "beam", "0123456789ab")
		if tc.lists != (err == nil) || (count == 1) != tc.lists || tc.lists && id != "external-catalog" {
			t.Fatalf("unexpected lookup %q %d %v", id, count, err)
		}
		if _, err := c.Call(context.Background(), "notebook.mine.list", nil); err == nil {
			t.Fatal("discovery widened general method allowlist")
		}
	}
}
