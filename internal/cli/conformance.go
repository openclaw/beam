package cli

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

type adapterEvidence struct {
	TestVersion int                  `json:"testVersion"`
	Adapter     string               `json:"adapter"`
	Advertised  adapter.Capabilities `json:"advertised"`
	Observed    []string             `json:"observed"`
}

// A fixture run never discovers conversations or sends instructions. Only an
// explicit mode authorizes creating a reference in the selected harness.
func testAdapter(ctx context.Context, dst adapter.Adapter, o options, in io.Reader) (any, error) {
	name, session, mode := o.values["fixture"], o.values["session"], o.values["mode"]
	checkingFixture := session != "" || mode != ""
	if checkingFixture != (name != "") {
		return nil, fmt.Errorf("pair --fixture FILE with --session ID, --mode stage|native, or both")
	}
	if mode != "" && mode != "stage" && mode != "native" {
		return nil, fmt.Errorf("fixture delivery requires --mode stage or native")
	}
	if o.values["out"] != "" && (mode != "stage" || o.values["in"] != "file") {
		return nil, fmt.Errorf("--out selects the manual artifact for --in file --mode stage")
	}
	var fixture packet.Packet
	if name != "" {
		var err error
		fixture, _, err = inputPacket(name, in)
		if err != nil {
			return nil, err
		}
	}
	c, err := caps(ctx, dst)
	if err != nil {
		return nil, err
	}
	if c.Recovery != "manual" && c.Recovery != "lookupByOperationId" {
		return nil, fmt.Errorf("invalid recovery capability")
	}
	for _, op := range c.Source {
		if op != "read" && op != "list" {
			return nil, fmt.Errorf("invalid source capability")
		}
	}
	for _, op := range c.Destination {
		if !slices.Contains([]string{"openCopy", "stageReference", "resolve", "send"}, op) {
			return nil, fmt.Errorf("invalid destination capability")
		}
	}
	report := adapterEvidence{1, o.values["in"], c, []string{"capabilities"}}
	checkItems := func(selection string) error {
		if !slices.Contains(c.Source, "read") {
			return adapter.Unsupported("read")
		}
		got, _, err := selected(ctx, dst, selection, clean.Options{})
		if err != nil {
			return err
		}
		if !sameFixtureItems(got.Items, fixture.Items) {
			return fmt.Errorf("selected items differ from the fixture")
		}
		return nil
	}
	if session != "" {
		if err := checkItems(session); err != nil {
			return nil, err
		}
		report.Observed = append(report.Observed, "selected-items-match")
	}
	if mode == "" {
		return report, nil
	}
	value, err := openWithCapabilities(ctx, dst, fixture, o, c)
	if err != nil {
		return nil, err
	}
	ref, ok := value.(adapter.OpenResult)
	if !ok {
		return nil, fmt.Errorf("expected a passive delivery receipt")
	}
	report.Observed = append(report.Observed, ref.State)
	if ref.State == "manual" {
		return report, nil
	}
	if slices.Contains(c.Destination, "resolve") {
		reopened, err := call[adapter.OpenResult](ctx, dst, adapter.Request{Op: "resolve", Handle: ref.Handle})
		if err != nil {
			return nil, err
		}
		if reopened.State != ref.State || reopened.Handle != ref.Handle || reopened.NativeSession != ref.NativeSession {
			return nil, fmt.Errorf("resolved reference differs from the created reference")
		}
		report.Observed = append(report.Observed, "same-reference-resolved")
	}
	if ref.State == "ready" && slices.Contains(c.Source, "read") {
		if err := checkItems(ref.NativeSession); err != nil {
			return nil, err
		}
		report.Observed = append(report.Observed, "reopened-items-match")
	}
	return report, nil
}

// A reviewed excerpt can begin with p2 after the first speaker was omitted.
// Cleaning a re-export allocates packet-local aliases again. Compare which
// items share a speaker while preserving text, order, roles and attribution.
func sameFixtureItems(got, want []packet.Item) bool {
	if len(got) != len(want) {
		return false
	}
	gotFirst, wantFirst := map[string]int{}, map[string]int{}
	for i, item := range got {
		expected := want[i]
		if item.Type != expected.Type || item.Text != expected.Text || (item.Speaker == "") != (expected.Speaker == "") {
			return false
		}
		if item.Speaker == "" {
			continue
		}
		if gotFirst[item.Speaker] != wantFirst[expected.Speaker] {
			return false
		}
		if gotFirst[item.Speaker] == 0 {
			gotFirst[item.Speaker], wantFirst[expected.Speaker] = i+1, i+1
		}
	}
	return true
}
