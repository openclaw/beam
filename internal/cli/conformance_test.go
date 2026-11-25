package cli

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

type fixtureProbe struct {
	ops         []string
	wrongHandle bool
	recovery    string
}

func (p *fixtureProbe) Call(_ context.Context, r adapter.Request) (any, error) {
	p.ops = append(p.ops, r.Op)
	switch r.Op {
	case "capabilities":
		return adapter.Capabilities{Protocol: 1, Adapter: "fixture", Version: "1", Source: []string{"read"}, Destination: []string{"stageReference", "resolve"}, Recovery: p.recovery}, nil
	case "read":
		return adapter.ReadResult{Candidate: clean.Candidate{Source: "fixture", Title: "test", UpdatedAt: "2026-01-02T03:04:05Z", Origin: clean.Origin{Adapter: "fixture", SourceHome: "test", NativeSession: "s"}, Completeness: "complete", Items: []clean.Item{{Type: "other", Text: "wrong items"}}}}, nil
	case "stageReference", "resolve":
		handle := "a"
		if p.wrongHandle && r.Op == "resolve" {
			handle = "b"
		}
		return adapter.OpenResult{State: "staged", Handle: handle, NextAction: "Open the reference"}, nil
	}
	return nil, fmt.Errorf("unexpected operation %s", r.Op)
}

func TestAdapterTestChecksFixtureAndResolvedIdentity(t *testing.T) {
	file := "../../conformance/packet/example.beam.json"
	for _, tc := range []struct {
		flags          []string
		recovery       string
		wrong, success bool
		ops            []string
	}{
		{nil, "manual", false, true, []string{"capabilities"}},
		{nil, "invalid", false, false, []string{"capabilities"}},
		{[]string{"--fixture", file}, "manual", false, false, nil},
		{[]string{"--fixture", file, "--session", "s"}, "manual", false, false, []string{"capabilities", "read"}},
		{[]string{"--fixture", file, "--mode", "stage"}, "manual", true, false, []string{"capabilities", "stageReference", "resolve"}},
		{[]string{"--fixture", file, "--mode", "stage"}, "manual", false, true, []string{"capabilities", "stageReference", "resolve"}},
	} {
		probe := &fixtureProbe{wrongHandle: tc.wrong, recovery: tc.recovery}
		code, b, _ := invoke(t, App{Adapters: Registry{"fixture": probe}}, "", append([]string{"adapter", "test", "--in", "fixture", "--json"}, tc.flags...)...)
		if (code == 0) != tc.success || !slices.Equal(probe.ops, tc.ops) {
			t.Fatalf("%v: %d %s %v", tc.flags, code, b, probe.ops)
		}
	}
}

func TestFixtureItemsUsePacketLocalSpeakers(t *testing.T) {
	items := func(a, b, c string) []packet.Item {
		return []packet.Item{{Type: "userMessage", Text: "note", Speaker: a}, {Type: "userMessage", Text: "note", Speaker: b}, {Type: "userMessage", Text: "note", Speaker: c}}
	}
	want := items("p2", "p7", "p2")
	if !sameFixtureItems(items("p1", "p2", "p1"), want) || sameFixtureItems(items("p1", "p1", "p1"), want) || sameFixtureItems(items("p1", "p2", "p3"), want) || sameFixtureItems(items("", "p2", ""), want) {
		t.Fatal("changed speaker grouping")
	}
}
