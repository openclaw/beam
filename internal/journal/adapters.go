package journal

import (
	"context"
	"database/sql"
	"errors"
)

// Adapter operations record the local intent before calling another executable.
// The digest binds retries to their original request without retaining transcript bytes.
type AdapterOperation struct {
	Target, ID, Op, Digest string
	Receipt                []byte
}

func (s *Store) ensureAdapterOperations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS beam_adapter_operations (
		target TEXT NOT NULL, id TEXT NOT NULL, op TEXT NOT NULL, digest TEXT NOT NULL, receipt BLOB,
		PRIMARY KEY(target,id)
	)`)
	return err
}

func (s *Store) AdapterOperation(ctx context.Context, target, id string) (AdapterOperation, bool, error) {
	if err := s.ensureAdapterOperations(ctx); err != nil {
		return AdapterOperation{}, false, err
	}
	r := AdapterOperation{Target: target, ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT op,digest,receipt FROM beam_adapter_operations WHERE target=? AND id=?`, target, id).Scan(&r.Op, &r.Digest, &r.Receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) BeginAdapter(ctx context.Context, r AdapterOperation) (AdapterOperation, bool, error) {
	if err := s.ensureAdapterOperations(ctx); err != nil {
		return r, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO beam_adapter_operations(target,id,op,digest) VALUES(?,?,?,?) ON CONFLICT(target,id) DO NOTHING`, r.Target, r.ID, r.Op, r.Digest)
	if err != nil {
		return r, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return r, false, err
	}
	if count == 1 {
		return r, true, nil
	}
	saved, ok, err := s.AdapterOperation(ctx, r.Target, r.ID)
	if err != nil {
		return r, false, err
	}
	if !ok || saved.Op != r.Op || saved.Digest != r.Digest {
		return r, false, ErrConflict
	}
	return saved, false, nil
}

func (s *Store) FinishAdapter(ctx context.Context, r AdapterOperation, receipt []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE beam_adapter_operations SET receipt=? WHERE target=? AND id=? AND op=? AND digest=? AND (receipt IS NULL OR receipt=?)`, receipt, r.Target, r.ID, r.Op, r.Digest, receipt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
