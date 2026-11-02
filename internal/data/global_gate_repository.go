package data

import (
	"context"
	"time"
)

func (s *GlobalGateRepository) State(ctx context.Context) (GlobalState, error) {
	var g GlobalState
	var rate int64
	if s.db.QueryRowContext(ctx, "SELECT token_paused,rate_until FROM delivery_gates").Scan(&g.TokenPaused, &rate) != nil {
		return g, ErrStorage
	}
	g.RateUntil = instant(rate)
	return g, nil
}
func (s *GlobalGateRepository) SetTokenPaused(ctx context.Context, paused bool) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET token_paused=?", paused); err != nil {
		return ErrStorage
	}
	return nil
}
func (s *GlobalGateRepository) ExtendRate(ctx context.Context, until time.Time) error {
	if until.IsZero() {
		return ErrInvalid
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET rate_until=MAX(rate_until,?)", until.UnixNano()); err != nil {
		return ErrStorage
	}
	return nil
}
