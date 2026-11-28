package clean

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func candidate(items ...Item) Candidate {
	return Candidate{Source: "codex", Origin: Origin{"codex", "fixture-home", "session-1"}, Title: "CSV importer", UpdatedAt: "2024-02-29T01:02:03Z", Items: items, Completeness: "complete"}
}

func TestCompactBudgetKeepsNewestItems(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{3, 3}, {10, 9}, {200, 9}} {
		items := make([]Item, tc.input)
		for i := range items {
			items[i] = Item{Type: "userMessage", Text: strconv.Itoa(i) + strings.Repeat("&", 5997)}
		}
		p, report, err := Build(candidate(items...), Options{})
		if err != nil || len(p.Items) != tc.want {
			t.Fatalf("%d input items: kept %d, want %d (err %v)", tc.input, len(p.Items), tc.want, err)
		}
		wantTruncated := tc.input > tc.want
		if report.Truncated != wantTruncated || p.Truncated != wantTruncated {
			t.Fatalf("%d input items: incorrect truncation state", tc.input)
		}
		for i, item := range p.Items {
			if item.Text != items[tc.input-tc.want+i].Text {
				t.Fatalf("%d input items: changed item %d", tc.input, i)
			}
		}
	}
}

func TestFitOmitsItemsThatAreBlankOnTheWire(t *testing.T) {
	items := []Item{
		{Type: "other", Text: "\ufeff"},
		{Type: "other", Text: strings.Repeat("\ufeff", packet.MaxItemChars) + "x"},
	}
	for range 10 {
		items = append(items, Item{Type: "other", Text: strings.Repeat("&", packet.MaxItemChars)})
	}
	p, report, err := Build(candidate(items...), Options{})
	if err != nil || report.Omissions != 2 || !report.Truncated || len(p.Items) != 9 {
		t.Fatalf("got %d items, report %+v, err %v", len(p.Items), report, err)
	}
}

func TestCleanerGolden(t *testing.T) {
	input, err := os.ReadFile("../../conformance/packet/read.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCandidate(input)
	if err != nil {
		t.Fatal(err)
	}
	p, r, err := Build(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r != (Report{}) {
		t.Fatalf("unexpected loss: %+v", r)
	}
	b, err := packet.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(b) {
		t.Fatal("golden packet bytes changed")
	}
}

func TestCleanEveryVisibleField(t *testing.T) {
	c := candidate(Item{Type: "userMessage", Text: "Please check raw.person@example.test\r\nThanks!", SpeakerID: "private-user-123"}, Item{Type: "agentMessage", Text: "[Beam asset omitted: file confidential.person@example.test.txt (1 KB)]"}, Item{Type: "other", Text: "Secret records\nAPI_TOKEN: |\n  synthetic-fragment-one\n  synthetic-fragment-two\nFinished"}, Item{Type: "agentMessage", Text: "more ALPHA42"}, Item{Type: "userMessage", Text: "sensitive user-selected turn", SpeakerID: "private-user-456"})
	c.Title = "Fix on /Users/example/project"
	c.SourceModel = &packet.Model{Provider: "fixture", Model: "private@example.test"}
	c.Completeness = "partial"
	p, r, err := Build(c, Options{OmitItems: []int{5}, RedactLiterals: []string{"ALPHA42"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := packet.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"example.test", "/Users/", "API_TOKEN", "fragment-one", "fragment-two", "ALPHA42", "private-user", "sensitive user-selected", "fixture-home", "session-1"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("leaked %s", secret)
		}
	}
	if p.Items[0].Speaker != "p1" || !strings.Contains(p.Items[1].Text, "[Beam asset omitted: file]") || !strings.Contains(p.Items[2].Text, "Finished") || !r.Truncated || r.Omissions < 2 || r.Redactions < 3 || r.LossyConversions != 1 {
		t.Fatalf("unexpected cleaned projection or counts: %+v", r)
	}
}

func TestVisibleRedactions(t *testing.T) {
	for _, s := range []string{
		"Authorization: Bearer fixture-credential-here", "--password example-value", "DB_PASS=\\\n  example-value", "PGPASSFILE=example-value",
		"PASSWORD: >-\n  example-value\n  second-line\nSafe", "-----BEGIN PRIVATE KEY-----\nexample-value",
		"key at /private/fixture/example-value", `file "C:\Some Path\example-value"`, "network https://example.test/?path=%252Fprivate%252Fexample-value",
		"run <system-reminder>example-value</system-reminder> now", "ß<environment_context>example-value</environment_context> end",
		"pass sk-fixtureabcdefghijklmnopqrst or bob@example.test", "phone +1 202 555 0126",
	} {
		r := Report{}
		out := cleanText(s, nil, &r)
		for _, sensitive := range []string{"fixture-credential", "example-value", "second-line", "sk-fixture", "bob@example.test", "202 555"} {
			if strings.Contains(out, sensitive) {
				t.Errorf("failed expected redaction for %q", s)
			}
		}
	}
	for _, s := range []string{"2026-12-04", "https://example.test/docs/api", "Keep 3 rows.", "2026.12.06"} {
		r := Report{}
		if got := cleanText(s, nil, &r); got != s {
			t.Errorf("expected %q got %q", s, got)
		}
	}
}

func TestFitAndFilter(t *testing.T) {
	var items []Item
	for range 210 {
		items = append(items, Item{Type: "userMessage", Text: strings.Repeat("🦊", 3100), SpeakerID: "same-source-user"})
	}
	items = append(items, Item{Type: "agentMessage", Text: "Most recent"})
	p, r, err := Build(candidate(items...), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := packet.Marshal(p)
	if err != nil || len(b) > packet.MaxBytes || !p.Truncated || !r.Truncated || len(p.Items) >= 200 || p.Items[len(p.Items)-1].Text != "Most recent" {
		t.Fatal("did not retain bounded newest tail")
	}
	runes := []rune(p.Items[0].Text)
	if len(runes) != 3000 || runes[2999] != '🦊' {
		t.Fatal("clipping split an astral scalar")
	}
	for _, c := range []Candidate{candidate(Item{Type: "toolResult", Text: "raw fixture"}), candidate(Item{Type: "agentMessage", Text: "<system-reminder>private</system-reminder>"})} {
		if _, _, err := Build(c, Options{}); err == nil {
			t.Fatal("accepted unsafe or empty projection")
		}
	}
	c := candidate(Item{Type: "agentMessage", Text: "visible"})
	b, _ = json.Marshal(c)
	if _, err := ParseCandidate([]byte(strings.Replace(string(b), `"title":`, `"title":"duplicate","title":`, 1))); err == nil {
		t.Fatal("accepted duplicate candidate key")
	}
	if _, _, err := Build(c, Options{OmitItems: []int{2}}); err == nil {
		t.Fatal("accepted invalid omission index")
	}
}

func TestOverlappingLiteralRedactions(t *testing.T) {
	c := candidate(Item{Type: "userMessage", Text: "XX BLUEBIRD"})
	literals := []string{"BLUE", "BLUEBIRD", "E"}
	for range 64 {
		literals = append(literals, "E")
	}
	p, r, err := Build(c, Options{RedactLiterals: literals})
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].Text != "XX [REDACTED]" || r.Redactions != 1 {
		t.Fatalf("unexpected literal projection %q, redactions=%d", p.Items[0].Text, r.Redactions)
	}
}

func TestGoalCommandSurvivesPathCleaning(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"/goal repair the importer", "/goal repair the importer"},
		{"resume /goal after review", "resume /goal after review"},
		{`"/goal repair the importer"`, `"/goal repair the importer"`},
		{"'/goal read /private/fixture'", "'/goal read [LOCAL_PATH]'"},
		{"/goal read /Users/example/task and /goal/private", "/goal read [LOCAL_PATH] and [LOCAL_PATH]"},
		{`"/goal/private" and /Users and /private`, `[LOCAL_PATH] and [LOCAL_PATH] and [LOCAL_PATH]`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			r := Report{}
			if got := cleanText(tc.input, nil, &r); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGoalSurvivesTranscriptTrimming(t *testing.T) {
	c := candidate()
	for range 210 {
		c.Items = append(c.Items, Item{Type: "userMessage", Text: strings.Repeat("x ", 2900)})
	}
	c.Goal = &packet.Goal{Objective: "/goal repair /private/fixture", Status: "active", TokensUsed: 40}
	p, report, err := Build(c, Options{})
	if err != nil || !report.Truncated || p.Version != 3 || p.Goal == nil || p.Goal.Objective != "/goal repair [LOCAL_PATH]" || p.Goal.TokensUsed != 40 {
		t.Fatalf("goal lost during trim: %+v / %+v / %v", p.Goal, report, err)
	}
	if c.Goal.Objective != "/goal repair /private/fixture" {
		t.Fatal("cleaning mutated the source goal")
	}
	c.Goal.Objective = strings.Repeat("x ", packet.MaxGoalChars)
	if _, _, err := Build(c, Options{}); err == nil {
		t.Fatal("silently clipped an oversized goal")
	}
}

func TestGoalOnlySession(t *testing.T) {
	c := candidate()
	c.Goal = &packet.Goal{Objective: "repair the importer", Status: "active"}
	p, _, err := Build(c, Options{})
	if err != nil || p.Goal == nil || p.Goal.Objective != c.Goal.Objective || len(p.Items) != 1 {
		t.Fatalf("goal-only export failed: %+v / %v", p, err)
	}
}
