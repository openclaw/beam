package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

type planProbe struct {
	requests     []adapter.Request
	capabilities adapter.Capabilities
}

func (p *planProbe) Call(_ context.Context, r adapter.Request) (any, error) {
	p.requests = append(p.requests, r)
	if r.Op != "capabilities" {
		return nil, fmt.Errorf("unexpected operation %s", r.Op)
	}
	return p.capabilities, nil
}

func TestPlanUsesOnlyCurrentCapabilities(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	probe := &planProbe{capabilities: adapter.Capabilities{Protocol: 1, Adapter: "codex", Version: "0.172.0", Destination: []string{"openCopy", "send"}, Recovery: "manual"}}
	app := App{Adapters: Registry{"third-party": probe}}
	code, _, _ := invoke(t, app, string(raw), "plan", "-", "--in", "third-party", "--expect-digest", "stale", "--json")
	if code != 5 || len(probe.requests) != 0 {
		t.Fatal(code, probe.requests)
	}
	code, out, _ := invoke(t, app, string(raw), "plan", "-", "--in", "third-party", "--expect-digest", packet.Digest(raw), "--json")
	var receipt struct {
		Result struct {
			Operation, Digest                string
			Visibility                       visibility
			StartsTurn, SupportsExplicitSend bool
		}
	}
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatal(err)
	}
	result := receipt.Result
	if code != 0 || len(probe.requests) != 1 || probe.requests[0].Op != "capabilities" || len(probe.requests[0].Packet.Items) != 0 || result.Operation != "openCopy" || result.Digest != packet.Digest(raw) || result.StartsTurn || !result.SupportsExplicitSend || result.Visibility.ModelContext != "unknown" {
		t.Fatalf("%d %s %+v", code, out, probe.requests)
	}
	probe.capabilities.Destination = nil
	code, _, _ = invoke(t, app, string(raw), "open", "-", "--in", "third-party", "--mode", "native", "--json")
	if code != 3 || len(probe.requests) != 2 || probe.requests[1].Op != "capabilities" {
		t.Fatal(code, probe.requests)
	}
	for _, cmd := range []string{"plan", "open"} {
		code, _, _ = invoke(t, App{}, string(raw), cmd, "-", "--in", "file", "--mode", "native", "--json")
		if code != 3 {
			t.Fatal(cmd, code)
		}
	}
	code, out, _ = invoke(t, App{}, string(raw), "plan", "-", "--in", "file", "--json")
	if err := json.Unmarshal(out, &receipt); err != nil || code != 0 || receipt.Result.Operation != "stageReference" || receipt.Result.Visibility.History != "manual-markdown" {
		t.Fatal(code, string(out), err)
	}
}
