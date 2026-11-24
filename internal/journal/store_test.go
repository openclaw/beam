package journal

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"path/filepath"
	"testing"
	"time"
)

func TestReplayedPublishFromEarlierJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoffs.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.Exec(`CREATE TABLE beam_pending (
 endpoint TEXT NOT NULL, kind TEXT NOT NULL, resource TEXT NOT NULL, id TEXT NOT NULL,
 body BLOB NOT NULL, condition TEXT NOT NULL, created INTEGER NOT NULL,
 PRIMARY KEY(endpoint,kind,resource), UNIQUE(endpoint,id));
 INSERT INTO beam_pending VALUES('endpoint','publish','beam','old',x'01','*',1);`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	p, found, err := s.Pending(ctx, "endpoint", "publish", "beam")
	if err != nil || !found || p.ID != "old" {
		t.Fatal("missing previous reservation", err)
	}
	if err := s.AllowPublishReplay(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearRejectedPublish(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Pending(ctx, "endpoint", "publish", "beam"); err != nil || !found {
		t.Fatal("discarded previously uncertain publish", err)
	}
	// An earlier binary can acknowledge the row using its original DELETE.
	if _, err := old.Exec(`DELETE FROM beam_pending`); err != nil {
		t.Fatal(err)
	}
	next := p
	next.ID = "new"
	if _, err := s.Begin(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := s.AllowPublishReplay(ctx, p); !errors.Is(err, ErrConflict) {
		t.Fatal("admitted a stale attempt for a replaced reservation", err)
	}
	if err := s.ClearRejectedPublish(ctx, p); err != nil {
		t.Fatal(err)
	}
	if saved, found, err := s.Pending(ctx, "endpoint", "publish", "beam"); err != nil || !found || saved.ID != next.ID {
		t.Fatal("discarded replacement reservation", err)
	}
	var oldBody, firstBody []byte
	if err := old.QueryRow(`SELECT body,first_publish_body FROM beam_pending WHERE id='new'`).Scan(&oldBody, &firstBody); err != nil || len(oldBody) != 0 || !bytes.Equal(firstBody, next.Body) {
		t.Fatal("earlier reader could dispatch an unguarded first attempt", err)
	}
	// A hard stop can leave the first-attempt bytes here. A fresh client reads
	// the frozen data and makes it visible to earlier versions before replay.
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	frozen, found, err := reopened.Pending(ctx, "endpoint", "publish", "beam")
	if err != nil || !found || !bytes.Equal(frozen.Body, next.Body) || frozen.ID != next.ID {
		t.Fatal("lost original crash recovery data", err)
	}
	if err := reopened.AllowPublishReplay(ctx, frozen); err != nil {
		t.Fatal(err)
	}
	if err := old.QueryRow(`SELECT body,first_publish_body FROM beam_pending WHERE id='new'`).Scan(&oldBody, &firstBody); err != nil || !bytes.Equal(oldBody, next.Body) || firstBody != nil {
		t.Fatal("earlier reader cannot resume an explicitly replayable intent", err)
	}
}

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
