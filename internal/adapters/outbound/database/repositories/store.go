package repositories

import (
	"context"
	"database/sql"
	"errors"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/platform/guard"
)

var ErrTransaction = errors.New("invalid transaction lifecycle operation")

var _ domain.Store = (*Store)(nil)

type Store struct {
	db                  *sql.DB
	inTx                bool
	trackedCases        *TrackedCaseRepository
	trackedCasesHistory *TrackedCasesHistoryRepository
	cooldowns           *CooldownRepository
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db:                  guard.Require("DB", db),
		trackedCases:        NewTrackedCaseRepository(db),
		trackedCasesHistory: NewTrackedCasesHistoryRepository(db),
		cooldowns:           NewCooldownRepository(db),
	}
}

func (this *Store) RunInTx(ctx context.Context, fn func(domain.Store) error) error {
	if this.inTx {
		return ErrTransaction
	}
	tx, err := this.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()

	store := &Store{
		db:                  this.db,
		inTx:                true,
		trackedCases:        NewTrackedCaseRepository(tx),
		trackedCasesHistory: NewTrackedCasesHistoryRepository(tx),
		cooldowns:           NewCooldownRepository(tx),
	}
	if err = fn(store); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrStorage
	}
	if err = tx.Commit(); err != nil {
		return ErrStorage
	}
	return nil
}

func (this *Store) TrackedCases() domain.TrackedCaseRepository {
	return this.trackedCases
}

func (this *Store) TrackedCasesHistory() domain.TrackedCasesHistoryRepository {
	return this.trackedCasesHistory
}

func (this *Store) Cooldowns() domain.CooldownRepository {
	return this.cooldowns
}
