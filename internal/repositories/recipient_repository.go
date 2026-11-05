package repositories

import "context"

type RecipientRepository struct{ db DBTX }

func (this *RecipientRepository) GetState(ctx context.Context, recipient int64) (RecipientState, error) {
	if recipient <= 0 {
		return RecipientState{}, ErrInvalid
	}
	var r RecipientState
	if this.db.QueryRowContext(ctx, `
		SELECT COALESCE((
			       SELECT pause
			         FROM recipients
			        WHERE recipient_id = ?
		       ), ''),
		       COALESCE((
			       SELECT blocked
			         FROM recipients
			        WHERE recipient_id = ?
		       ), 0)
	`, recipient, recipient).Scan(&r.Pause, &r.Blocked) != nil {
		return r, ErrStorage
	}
	return r, nil
}

func (this *RecipientRepository) SetState(ctx context.Context, recipient int64, state RecipientState) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	if _, err := this.db.ExecContext(ctx, `
		INSERT INTO recipients (recipient_id, pause, blocked)
		VALUES (?, ?, ?)
		ON CONFLICT (recipient_id)
		DO UPDATE SET pause = excluded.pause,
		              blocked = excluded.blocked
	`, recipient, state.Pause, state.Blocked); err != nil {
		return ErrStorage
	}
	return nil
}
