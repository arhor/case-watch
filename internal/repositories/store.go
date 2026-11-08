package repositories

import (
	"context"
	"database/sql"
	"errors"
	"os"
)

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Store struct {
	db          *sql.DB
	lock        *os.File
	inTx        bool
	Enrollments *EnrollmentRepository
	History     *HistoryRepository
	Cooldowns   *CooldownRepository
}

func NewStore(ctx context.Context, path string) (*Store, error) {
	db, err := openDB(ctx, path)
	if err != nil {
		return nil, err
	}
	return newStore(db, nil), nil
}

// NewExclusiveStore prevents concurrent bot instances from using the same
// database. Ordinary NewStore callers retain SQLite's native multi-process access.
func NewExclusiveStore(ctx context.Context, path string) (*Store, error) {
	db, lock, err := openExclusiveDB(ctx, path)
	if err != nil {
		return nil, err
	}
	return newStore(db, lock), nil
}

func newStore(db *sql.DB, lock *os.File) *Store {
	return &Store{
		db:          db,
		lock:        lock,
		Enrollments: &EnrollmentRepository{db: db},
		History:     &HistoryRepository{db: db},
		Cooldowns:   &CooldownRepository{db: db},
	}
}

func (this *Store) Close() error {
	if this.inTx {
		return ErrTransaction
	}
	err := this.db.Close()
	if this.lock != nil {
		err = errors.Join(err, this.lock.Close())
	}
	return err
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
		db:          this.db,
		inTx:        true,
		Enrollments: &EnrollmentRepository{db: tx},
		History:     &HistoryRepository{db: tx},
		Cooldowns:   &CooldownRepository{db: tx},
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
