package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/arhor/case-watch/internal/database/repositories"
)

var ErrTransaction = errors.New("invalid transaction lifecycle operation")

type Store struct {
	db          *sql.DB
	inTx        bool
	Enrollments *repositories.EnrollmentRepository
	History     *repositories.HistoryRepository
	Cooldowns   *repositories.CooldownRepository
}

func New(db *sql.DB) *Store {
	return &Store{
		db:          db,
		Enrollments: repositories.NewEnrollmentRepository(db),
		History:     repositories.NewHistoryRepository(db),
		Cooldowns:   repositories.NewCooldownRepository(db),
	}
}

func (this *Store) RunInTx(ctx context.Context, fn func(*Store) error) error {
	if this.inTx {
		return ErrTransaction
	}
	tx, err := this.db.BeginTx(ctx, nil)
	if err != nil {
		return repositories.ErrStorage
	}
	defer tx.Rollback()

	txStore := &Store{
		db:          this.db,
		inTx:        true,
		Enrollments: repositories.NewEnrollmentRepository(tx),
		History:     repositories.NewHistoryRepository(tx),
		Cooldowns:   repositories.NewCooldownRepository(tx),
	}
	if err = fn(txStore); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return repositories.ErrStorage
	}
	if err = tx.Commit(); err != nil {
		return repositories.ErrStorage
	}
	return nil
}
