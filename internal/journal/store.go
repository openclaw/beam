// Package journal records receiver operations before a request can change remote state.
package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("another operation owns this journal entry")

type Store struct{ db *sql.DB }

func DefaultPath() (string, error) {
	if p := os.Getenv("BEAM_STATE"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "openclaw-beam", "handoffs.sqlite"), nil
}

func Open(path string) (*Store, error) {
	var err error
	if path == "" {
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := prepareJournal(path); err != nil {
		return nil, err
	}
	name := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(name) > 1 && name[1] == ':' {
		name = "/" + name
	}
	dsn := url.URL{Scheme: "file", Path: name, RawQuery: url.Values{"_pragma": {"busy_timeout(5000)"}, "_txlock": {"immediate"}}.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize Beam journal: %w", err)
	}
	return &Store{db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func initialize(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	var hasFirstBody bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('beam_pending') WHERE name='first_publish_body')`).Scan(&hasFirstBody); err != nil {
		return err
	}
	if !hasFirstBody {
		if _, err := tx.Exec(`ALTER TABLE beam_pending ADD COLUMN first_publish_body BLOB`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const schema = `
CREATE TABLE IF NOT EXISTS handoffs (
 endpoint TEXT NOT NULL, beam_id TEXT NOT NULL, payload TEXT,
 publication_id TEXT, receipt TEXT, session_key TEXT, PRIMARY KEY(endpoint,beam_id)
);
CREATE TABLE IF NOT EXISTS beam_pending (
 endpoint TEXT NOT NULL, kind TEXT NOT NULL, resource TEXT NOT NULL, id TEXT NOT NULL,
 body BLOB NOT NULL, condition TEXT NOT NULL, created INTEGER NOT NULL, first_publish_body BLOB,
 PRIMARY KEY(endpoint,kind,resource), UNIQUE(endpoint,id)
);
CREATE TABLE IF NOT EXISTS beam_transfers (
 endpoint TEXT NOT NULL, beam_id TEXT NOT NULL, id TEXT NOT NULL,
 generation TEXT NOT NULL, revision TEXT NOT NULL, digest TEXT NOT NULL, url TEXT NOT NULL,
 api_url TEXT NOT NULL, packet BLOB, receipt BLOB,
 PRIMARY KEY(endpoint,beam_id)
);
CREATE TABLE IF NOT EXISTS beam_sent (
 endpoint TEXT NOT NULL, id TEXT NOT NULL, resource TEXT NOT NULL, digest TEXT NOT NULL,
 PRIMARY KEY(endpoint,id)
);`

type Pending struct {
	Endpoint, Kind, Resource, ID, Condition string
	Body                                    []byte
	Created                                 time.Time
}

func (s *Store) Pending(ctx context.Context, endpoint, kind, resource string) (Pending, bool, error) {
	return scanPending(s.db.QueryRowContext(ctx, selectPending, endpoint, kind, resource), Pending{Endpoint: endpoint, Kind: kind, Resource: resource})
}

const selectPending = `SELECT id,coalesce(first_publish_body,body),condition,created FROM beam_pending WHERE endpoint=? AND kind=? AND resource=?`

func scanPending(row *sql.Row, p Pending) (Pending, bool, error) {
	var timestamp int64
	err := row.Scan(&p.ID, &p.Body, &p.Condition, &timestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	p.Created = time.Unix(timestamp, 0)
	return p, err == nil, err
}

// Begin persists the first request; concurrent callers receive the winner's exact body and precondition.
func (s *Store) Begin(ctx context.Context, p Pending) (Pending, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Pending{}, err
	}
	defer tx.Rollback()
	body, firstPublishBody := p.Body, []byte(nil)
	if p.Kind == "publish" {
		// An older client sends only when the legacy body equals its selected packet.
		// Keep that slot empty until a second PUT makes first-attempt rejection ambiguous.
		body, firstPublishBody = []byte{}, p.Body
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO beam_pending(endpoint,kind,resource,id,body,condition,created,first_publish_body) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(endpoint,kind,resource) DO NOTHING`, p.Endpoint, p.Kind, p.Resource, p.ID, body, p.Condition, p.Created.Unix(), firstPublishBody); err != nil {
		return Pending{}, err
	}
	saved, _, err := scanPending(tx.QueryRowContext(ctx, selectPending, p.Endpoint, p.Kind, p.Resource), p)
	if err != nil {
		return Pending{}, err
	}
	return saved, tx.Commit()
}

func (s *Store) Clear(ctx context.Context, p Pending) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM beam_pending WHERE endpoint=? AND kind=? AND resource=? AND id=?`, p.Endpoint, p.Kind, p.Resource, p.ID)
	return err
}

// AllowPublishReplay restores the original bytes for all client versions. Once a
// replay might dispatch, a refusal of one attempt cannot retire the intent.
func (s *Store) AllowPublishReplay(ctx context.Context, p Pending) error {
	return exactlyOne(s.db.ExecContext(ctx, `UPDATE beam_pending SET body=coalesce(first_publish_body,body),first_publish_body=NULL
 WHERE endpoint=? AND kind='publish' AND resource=? AND id=?`, p.Endpoint, p.Resource, p.ID))
}

func (s *Store) ClearRejectedPublish(ctx context.Context, p Pending) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM beam_pending WHERE endpoint=? AND kind='publish' AND resource=? AND id=? AND first_publish_body IS NOT NULL`, p.Endpoint, p.Resource, p.ID)
	return err
}

func (s *Store) Sent(ctx context.Context, endpoint, id string) (resource, digest string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT resource,digest FROM beam_sent WHERE endpoint=? AND id=?`, endpoint, id).Scan(&resource, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return resource, digest, err == nil, err
}

// SentUUID returns the original spelling as well as the fingerprint. Older HTTP
// clients included the supplied UUID case in their request bytes.
func (s *Store) SentUUID(ctx context.Context, endpoint, id string) (original, resource, digest string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id,resource,digest FROM beam_sent WHERE endpoint=? AND id=? COLLATE NOCASE`, endpoint, id).Scan(&original, &resource, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	return original, resource, digest, err == nil, err
}

func (s *Store) ConfirmSend(ctx context.Context, p Pending, digest string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO beam_sent(endpoint,id,resource,digest) VALUES(?,?,?,?) ON CONFLICT(endpoint,id) DO NOTHING`, p.Endpoint, p.ID, p.Resource, digest); err != nil {
		return err
	}
	var resource, actual string
	if err := tx.QueryRowContext(ctx, `SELECT resource,digest FROM beam_sent WHERE endpoint=? AND id=?`, p.Endpoint, p.ID).Scan(&resource, &actual); err != nil {
		return err
	}
	if resource != p.Resource || actual != digest {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM beam_pending WHERE endpoint=? AND kind=? AND resource=? AND id=?`, p.Endpoint, p.Kind, p.Resource, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}
