package data

import "context"

// RunInTx binds every repository to one transaction. See ADR 0011:
// docs/adr/0011_move-transaction-boundaries-to-services.md.
// Use only the supplied Store inside fn; handles must not escape the callback.
func (s *Store) RunInTx(ctx context.Context, fn func(*Store) error) error {
	if s.inTx {
		return ErrTransaction
	}
	if fn == nil {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if err = fn(newStore(s.db, tx, true)); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
