package storage

import (
	"context"
	"github.com/arhor/case-watch/internal/application"
)

// RewriteCredentials applies an offline, all-or-nothing credential rewrite.
// Immutable IDs, ownership, history and queue rows remain untouched. Callers hold
// the process maintenance lock and persist both keys before entering this method.
func (s *Store) RewriteCredentials(ctx context.Context, rewrite func(application.SealedCredentials) (application.SealedCredentials, error)) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,credential_version,key_id,payload FROM enrollments ORDER BY id`)
	if e != nil {
		return ErrStorage
	}
	var records []application.SealedCredentials
	for rows.Next() {
		var r application.SealedCredentials
		if rows.Scan(&r.ID, &r.FormatVersion, &r.KeyID, &r.Payload) != nil {
			rows.Close()
			return ErrStorage
		}
		records = append(records, r)
	}
	e = rows.Err()
	ce := rows.Close()
	if e != nil || ce != nil {
		return ErrStorage
	}
	for _, r := range records {
		n, e := rewrite(r)
		if e != nil {
			return ErrStorage
		}
		if n.ID != r.ID || n.FormatVersion != 1 || n.KeyID == "" || len(n.Payload) == 0 {
			return ErrInvalid
		}
		result, e := tx.ExecContext(ctx, `UPDATE enrollments SET credential_version=?,key_id=?,payload=? WHERE id=?`, n.FormatVersion, n.KeyID, n.Payload, string(r.ID))
		if e != nil {
			return ErrStorage
		}
		count, e := result.RowsAffected()
		if e != nil || count != 1 {
			return ErrStorage
		}
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
