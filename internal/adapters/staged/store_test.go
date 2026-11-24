package staged

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

func TestStagePersistsAndBindsExactPacket(t *testing.T) {
	id := "operation"
	s := Store{Root: t.TempDir(), Adapter: "claude"}
	q := adapter.Request{OperationID: id, Packet: packet.Packet{Version: 2, BeamID: strings.Repeat("a", 32), Source: "file", Title: "Title", UpdatedAt: "2026-11-23T00:00:00Z", Items: []packet.Item{{Type: "userMessage", Text: "visible text"}}}}
	res, err := s.Stage(q)
	if err != nil || res.State != "staged" {
		t.Fatal(res, err)
	}
	again, err := s.Lookup(id)
	if err != nil || again != res {
		t.Fatal(again, err)
	}
	if _, err := s.Resolve("../../outside"); err == nil {
		t.Fatal("accepted path traversal")
	}
	q.Packet.Title = "later"
	if _, err := s.Stage(q); err == nil {
		t.Fatal("reused handle for different input")
	}
	bytes1, err := os.ReadFile(res.Artifact)
	if err != nil || !bytes.Contains(bytes1, []byte("visible text")) {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(res.Artifact)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(id)
	if err != nil || got.State != "unknown" {
		t.Fatal(got, err)
	}
}

func TestStageLeavesExistingReferencesAtCapacity(t *testing.T) {
	s := Store{Root: t.TempDir(), Adapter: "pi"}
	q := adapter.Request{OperationID: "already-staged", Packet: packet.Packet{Version: 2, BeamID: strings.Repeat("a", 32), Source: "fixture", Title: "fixture", UpdatedAt: "2026-11-23T00:00:00Z", Items: []packet.Item{{Type: "userMessage", Text: "fixture"}}}}
	got, err := s.Stage(q)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(filepath.Dir(got.Artifact))
	for i := 1; i < maxReferences; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("reference-%d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Stage(q); err != nil {
		t.Fatal("existing operation at capacity:", err)
	}
	q.OperationID = "new"
	if _, err := s.Stage(q); err == nil {
		t.Fatal("accepted new operation at capacity")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != maxReferences {
		t.Fatal("failed stage left extra artifact")
	}
}
