package clean

import (
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func BenchmarkConversation(b *testing.B) {
	c := candidate()
	const paragraph = "We tried the proposed change on a small example. The import kept its original order, including the final row.\n"
	for i := range 160 {
		item := Item{Type: "agentMessage", Text: strings.Repeat(paragraph, 2)}
		if i%2 == 0 {
			item.Type = "userMessage"
			item.SpeakerID = "participant"
		}
		if i%10 == 0 {
			item.Text += "See https://example.test/docs, or ask the owner at person@example.test."
		}
		c.Items = append(c.Items, item)
	}
	p, _, err := Build(c, Options{})
	if err != nil {
		b.Fatal(err)
	}
	data, err := packet.Marshal(p)
	if err != nil || p.Truncated {
		b.Fatal("benchmark fixture must fit in one packet", err)
	}
	b.Run("Clean", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := Build(c, Options{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Parse", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := packet.Parse(data); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Render", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			packet.Render(p)
		}
	})
}
