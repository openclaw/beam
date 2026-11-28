package native

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func TestGoalReferenceDoesNotConsumeTranscriptItems(t *testing.T) {
	p := packet.Packet{Goal: &packet.Goal{Objective: "repair\nthen verify", Status: "active"}}
	for i := range packet.MaxItems {
		p.Items = append(p.Items, packet.Item{Type: "userMessage", Text: fmt.Sprintf("message %d", i)})
	}
	for range 3 {
		frame := Frame(p)
		rows := ReferenceItems(frame)
		if len(rows) != packet.MaxItems || rows[0].Text != p.Items[0].Text || !reflect.DeepEqual(ReferenceGoal(frame), p.Goal) {
			t.Fatal("reference import duplicated goal or dropped history")
		}
		for i, row := range rows {
			p.Items[i].Text = row.Text
		}
	}
}
