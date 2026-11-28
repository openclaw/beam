package packet

import (
	"encoding/json"
	"strings"
	"testing"
)

func goalPacket(t *testing.T) Packet {
	t.Helper()
	p, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(12000)
	p.Version = 3
	p.Goal = &Goal{Objective: "Finish the reviewed repair", Status: "paused", TokenBudget: &budget, TokensUsed: 4000}
	return p
}

func TestGoalPacketBoundary(t *testing.T) {
	p := goalPacket(t)
	for _, status := range []string{"active", "paused", "blocked", "usageLimited", "budgetLimited", "complete"} {
		p.Goal.Status = status
		raw, err := Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(raw)
		if err != nil || got.Goal == nil || got.Goal.Status != status || got.Goal.TokensUsed != 4000 || *got.Goal.TokenBudget != 12000 {
			t.Fatalf("goal round trip: %+v, %v", got, err)
		}
	}
	raw, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ from, to string }{
		{`"version":3`, `"version":2`},
		{`"status":"complete"`, `"status":"running"`},
		{`"tokensUsed":4000`, `"tokensUsed":-1`},
		{`"tokensUsed":4000`, `"tokensUsed":1.5`},
		{`"tokensUsed":4000`, `"tokensUsed":9007199254740992`},
		{`"tokensUsed":4000`, `"tokensUsed":null`},
		{`,"tokensUsed":4000`, ``},
		{`"tokenBudget":12000`, `"tokenBudget":0`},
		{`"tokenBudget":12000`, `"tokenBudget":9007199254740992`},
		{`"objective":"Finish the reviewed repair"`, `"objective":" "`},
		{`"objective":"Finish the reviewed repair"`, `"objective":"` + strings.Repeat("🦊", MaxGoalChars+1) + `"`},
		{`"status":"complete"`, `"status":"complete","id":"native-goal"`},
		{`"tokensUsed":4000`, `"tokensUsed":4000,"tokensUsed":4000`},
	} {
		t.Run(tc.to, func(t *testing.T) {
			if _, err := Parse([]byte(strings.Replace(string(raw), tc.from, tc.to, 1))); err == nil {
				t.Fatal("accepted invalid goal")
			}
		})
	}
	p.Goal.Objective = strings.Repeat("🦊", MaxGoalChars)
	p.Goal.TokensUsed = MaxSafeInteger
	p.Goal.TokenBudget = nil
	if _, err := Marshal(p); err != nil {
		t.Fatal("rejected valid scalar limit or safe counter", err)
	}
}

func TestGoalProjectionPreservesIntent(t *testing.T) {
	p := goalPacket(t)
	for _, version := range []int{1, 2} {
		if _, err := Projection(p, version); err == nil || !strings.Contains(err.Error(), "goal") {
			t.Fatalf("projection to %d silently dropped goal", version)
		}
	}
	if _, err := V1Projection(p); err == nil {
		t.Fatal("legacy projection silently dropped goal")
	}
	raw, err := Projection(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil || got.Goal.Objective != p.Goal.Objective {
		t.Fatal("v3 projection lost goal", err)
	}
	p.Goal = nil
	for _, version := range []int{2, 3} {
		raw, err := Projection(p, version)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded["version"] != float64(version) {
			t.Fatal("incorrect projection version", err)
		}
	}
	if _, err := Projection(p, 4); err == nil {
		t.Fatal("accepted unsupported projection")
	}
}

func TestGoalRenderRemainsHistorical(t *testing.T) {
	p := goalPacket(t)
	p.Goal.Objective = "repair\n![remote](https://example.test)\n\u202e"
	got := Render(p)
	for _, text := range []string{"Historical goal (reference only)", ">     /goal repair", ">     ![remote]", ">     Source status: paused", ">     Tokens used at source: 4000", ">     Source token budget: 12000", `\u202e`} {
		if !strings.Contains(got, text) {
			t.Fatalf("historical rendering missing %q", text)
		}
	}
	if strings.Contains(got, "\u202e") {
		t.Fatal("goal escaped quoted reference")
	}
}
