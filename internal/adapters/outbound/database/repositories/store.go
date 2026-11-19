package repositories

import (
	"context"
	"database/sql"
	"errors"

	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/platform/guard"
)

var ErrTransaction = errors.New("invalid transaction lifecycle operation")

var _ models.Store = (*Store)(nil)

type Store struct {
	db          *sql.DB
	inTx        bool
	enrollments *EnrollmentRepository
	history     *HistoryRepository
	cooldowns   *CooldownRepository
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db:          guard.Require("DB", db),
		enrollments: NewEnrollmentRepository(db),
		history:     NewHistoryRepository(db),
		cooldowns:   NewCooldownRepository(db),
	}
}

func (this *Store) RunInTx(ctx context.Context, fn func(models.Store) error) error {
	if this.inTx {
		return ErrTransaction
	}
	tx, err := this.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()

	store := &Store{
		db:          this.db,
		inTx:        true,
		enrollments: NewEnrollmentRepository(tx),
		history:     NewHistoryRepository(tx),
		cooldowns:   NewCooldownRepository(tx),
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

func (this *Store) Enrollments() models.EnrollmentRepository { return this.enrollments }

func (this *Store) History() models.HistoryRepository { return this.history }

func (this *Store) Cooldowns() models.CooldownRepository { return this.cooldowns }
