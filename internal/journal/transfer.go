package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type LegacyRow struct {
	Payload, Receipt          []byte
	PublicationID, SessionKey string
}

func (s *Store) Legacy(ctx context.Context, endpoint, beamID string) (LegacyRow, bool, error) {
	var row LegacyRow
	err := s.db.QueryRowContext(ctx, `SELECT payload,receipt,coalesce(publication_id,''),coalesce(session_key,'') FROM handoffs WHERE endpoint=? AND beam_id=?`, endpoint, beamID).Scan(&row.Payload, &row.Receipt, &row.PublicationID, &row.SessionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *Store) BeginLegacy(ctx context.Context, endpoint, beamID string, body []byte) (LegacyRow, error) {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO handoffs(endpoint,beam_id,payload) VALUES(?,?,?)`, endpoint, beamID, string(body))
	if err != nil {
		return LegacyRow{}, err
	}
	row, ok, err := s.Legacy(ctx, endpoint, beamID)
	if err == nil && !ok {
		err = ErrConflict
	}
	return row, err
}

func (s *Store) SetLegacyPublication(ctx context.Context, endpoint, beamID, generation string) error {
	return exactlyOne(s.db.ExecContext(ctx, `UPDATE handoffs SET publication_id=? WHERE endpoint=? AND beam_id=? AND (publication_id IS NULL OR publication_id=?)`, generation, endpoint, beamID, generation))
}

func (s *Store) CompleteLegacy(ctx context.Context, endpoint, beamID, generation string, receipt []byte) error {
	return exactlyOne(s.db.ExecContext(ctx, `UPDATE handoffs SET receipt=json_patch(coalesce(receipt,'{}'),?),payload=NULL,publication_id=? WHERE endpoint=? AND beam_id=? AND (publication_id IS NULL OR publication_id=?) AND (receipt IS NULL OR json_extract(receipt,'$.handoff.id')=?)`, string(receipt), generation, endpoint, beamID, generation, generation))
}

func (s *Store) ForgetUnpublishedLegacy(ctx context.Context, endpoint, beamID string) error {
	return exactlyOne(s.db.ExecContext(ctx, `DELETE FROM handoffs WHERE endpoint=? AND beam_id=? AND receipt IS NULL AND publication_id IS NULL AND payload IS NOT NULL`, endpoint, beamID))
}

func (s *Store) SaveLegacyDestination(ctx context.Context, endpoint, beamID, key, id string) error {
	return exactlyOne(s.db.ExecContext(ctx, `UPDATE handoffs SET session_key=?,receipt=json_set(receipt,'$.sessionId',?) WHERE endpoint=? AND beam_id=? AND receipt IS NOT NULL AND (session_key IS NULL OR session_key=?) AND (json_extract(receipt,'$.sessionId') IS NULL OR json_extract(receipt,'$.sessionId')=?)`, key, id, endpoint, beamID, key, id))
}

func exactlyOne(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

type Transfer struct {
	Endpoint, BeamID, ID, Generation, Revision, Digest, URL, APIURL string
	Packet, Receipt                                                 []byte
}

func getTransfer(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, endpoint, beamID string) (Transfer, bool, error) {
	t := Transfer{Endpoint: endpoint, BeamID: beamID}
	err := q.QueryRowContext(ctx, `SELECT id,generation,revision,digest,url,api_url,packet,receipt FROM beam_transfers WHERE endpoint=? AND beam_id=?`, endpoint, beamID).Scan(&t.ID, &t.Generation, &t.Revision, &t.Digest, &t.URL, &t.APIURL, &t.Packet, &t.Receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (s *Store) Transfer(ctx context.Context, endpoint, beamID string) (Transfer, bool, error) {
	return getTransfer(ctx, s.db, endpoint, beamID)
}

// BeginTransfer reserves the v2 generation and fences v1 hook writers in one commit.
func (s *Store) BeginTransfer(ctx context.Context, t Transfer) (Transfer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback()
	saved, found, err := getTransfer(ctx, tx, t.Endpoint, t.BeamID)
	if err != nil {
		return Transfer{}, err
	}
	if found {
		return saved, nil
	}
	r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO handoffs(endpoint,beam_id) VALUES(?,?)`, t.Endpoint, t.BeamID)
	if err := exactlyOne(r, err); err != nil {
		return Transfer{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO beam_transfers(endpoint,beam_id,id,generation,revision,digest,url,api_url,packet) VALUES(?,?,?,?,?,?,?,?,?)`, t.Endpoint, t.BeamID, t.ID, t.Generation, t.Revision, t.Digest, t.URL, t.APIURL, t.Packet)
	if err != nil {
		return Transfer{}, err
	}
	return t, tx.Commit()
}

func (s *Store) CompleteTransfer(ctx context.Context, t Transfer, receipt json.RawMessage) error {
	return exactlyOne(s.db.ExecContext(ctx, `UPDATE beam_transfers SET packet=NULL,receipt=? WHERE endpoint=? AND beam_id=? AND id=? AND generation=? AND revision=? AND digest=?`, []byte(receipt), t.Endpoint, t.BeamID, t.ID, t.Generation, t.Revision, t.Digest))
}
