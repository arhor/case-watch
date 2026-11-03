package data

import (
	"context"
	"time"
)

func (this *GlobalGateRepository) GetState(ctx context.Context) (GlobalState, error) {
	var g GlobalState
	var rate int64
	if this.db.QueryRowContext(ctx, `
		SELECT rate_until
		  FROM delivery_gates
	`).Scan(&rate) != nil {
		return g, ErrStorage
	}
	g.RateUntil = instant(rate)
	return g, nil
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
