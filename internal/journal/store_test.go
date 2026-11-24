package journal

import (
	"context"
	"errors"

	"path/filepath"
	"testing"
	"time"
)

func TestRestartAndLegacyFence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "handoffs.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := Pending{Endpoint: "endpoint", Kind: "publish", Resource: "beam", ID: "first", Body: []byte("reviewed"), Condition: "*", Created: time.Now()}
	if _, err := s.Begin(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p.ID = "second"
	p.Body = []byte("changed")
	saved, err := s.Begin(ctx, p)
	if err != nil || saved.ID != "first" || string(saved.Body) != "reviewed" {
		t.Fatalf("lost initial operation: %+v %v", saved, err)
	}
	tr := Transfer{Endpoint: "endpoint", BeamID: "beam", ID: "release", Generation: "gen", Revision: "etag", Digest: "digest", Packet: saved.Body}
	if _, err := s.BeginTransfer(ctx, tr); err != nil {
		t.Fatal(err)
	}
	row, found, err := s.Legacy(ctx, "endpoint", "beam")
	if err != nil || !found || row.Receipt != nil || row.Payload != nil {
		t.Fatalf("legacy fence missing: %+v %v", row, err)
	}
	if err := s.CompleteTransfer(ctx, tr, []byte(`{"state":"released"}`)); err != nil {
		t.Fatal(err)
	}
	tr, _, err = s.Transfer(ctx, "endpoint", "beam")
	if err != nil || tr.Packet != nil || len(tr.Receipt) == 0 {
		t.Fatalf("incomplete receipt: %+v %v", tr, err)
	}
	assertPrivateFile(t, path)
	// Inspect the actual SQLite rollback journal while a write is uncommitted.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE beam_pending SET condition='pending'"); err != nil {
		t.Fatal(err)
	}
	assertPrivateFile(t, path+"-journal")
}
func TestExistingLegacyRowCannotBeClaimed(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "handoffs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.BeginLegacy(ctx, "endpoint", "beam", []byte("frozen")); err != nil {
		t.Fatal(err)
	}
	_, err = s.BeginTransfer(ctx, Transfer{Endpoint: "endpoint", BeamID: "beam", ID: "new"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("v2 overwrote legacy fence: %v", err)
	}
	if err := s.SetLegacyPublication(ctx, "endpoint", "beam", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLegacyPublication(ctx, "endpoint", "beam", "new"); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced original generation")
	}
}
