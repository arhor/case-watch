package repositories

import (
	"context"
	"database/sql"
	"errors"
)

var ErrTransaction = errors.New("invalid transaction lifecycle operation")

type Store struct {
	db          *sql.DB
	inTx        bool
	Enrollments *EnrollmentRepository
	History     *HistoryRepository
	Cooldowns   *CooldownRepository
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db:          db,
		Enrollments: NewEnrollmentRepository(db),
		History:     NewHistoryRepository(db),
		Cooldowns:   NewCooldownRepository(db),
	}
}

func (this *Store) RunInTx(ctx context.Context, fn func(*Store) error) error {
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
		Enrollments: NewEnrollmentRepository(tx),
		History:     NewHistoryRepository(tx),
		Cooldowns:   NewCooldownRepository(tx),
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
