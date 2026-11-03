package data

import (
	"context"
	"time"
)

func (this *GlobalGateRepository) GetState(ctx context.Context) (GlobalState, error) {
	var g GlobalState
	var rate int64
	if this.db.QueryRowContext(ctx, `
		SELECT token_paused, rate_until
		  FROM delivery_gates
	`).Scan(&g.TokenPaused, &rate) != nil {
		return g, ErrStorage
	}
	g.RateUntil = instant(rate)
	return g, nil
}
func (s *GlobalGateRepository) SetTokenPaused(ctx context.Context, paused bool) error {
	if _, err := this.db.ExecContext(ctx, `
		UPDATE delivery_gates
		   SET token_paused = ?
	`, paused); err != nil {
		return ErrStorage
	}
	return nil
}

func (this *GlobalGateRepository) ExtendRateUntil(ctx context.Context, until time.Time) error {
	if until.IsZero() {
		return ErrInvalid
	}
	if _, err := this.db.ExecContext(ctx, `
		UPDATE delivery_gates
		   SET rate_until = MAX(rate_until, ?)
	`, until.UnixNano()); err != nil {
		return ErrStorage
	}
	return nil
}
