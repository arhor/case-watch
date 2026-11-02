package data

import (
	"context"
	"database/sql"
)

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Store struct {
	db               *sql.DB
	inTx bool
	Enrollments      *EnrollmentRepository
	History          *HistoryRepository
	Pending          *PendingDeliveryRepository
	Guards           *CheckGuardRepository
	Recipients       *RecipientRepository
	Gates            *GlobalGateRepository
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db:          db,
		Enrollments: &EnrollmentRepository{db: db},
		History:     &HistoryRepository{db: db},
		Pending:     &PendingDeliveryRepository{db: db},
		Guards:      &CheckGuardRepository{db: db},
		Recipients:  &RecipientRepository{db: db},
		Gates:       &GlobalGateRepository{db: db},
	}
}

func (this *Store) Close() error {
	if this.inTx {
		return ErrTransaction
	}
	if err := this.db.Close(); err != nil {
		return err
	}
	return nil
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

	txStore := &Store{
		db:               this.db,
		inTx: true,
		Enrollments:      &EnrollmentRepository{db: tx},
		History:          &HistoryRepository{db: tx},
		Pending:          &PendingDeliveryRepository{db: tx},
		Guards:           &CheckGuardRepository{db: tx},
		Recipients:       &RecipientRepository{db: tx},
		Gates:            &GlobalGateRepository{db: tx},
	}

	if err = fn(txStore); err != nil {
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
