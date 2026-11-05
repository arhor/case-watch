package service

import (
	"context"

	"github.com/arhor/case-watch/internal/data"
)

// Maintain opens an existing database only while the service is stopped.
func Maintain(ctx context.Context, db string, fn func(*data.Store) error) (result error) {
	db, err := DatabasePath(db)
	if err != nil {
		return err
	}

	lock, err := Acquire(db)
	if err != nil {
		return err
	}
	defer lock.Close()

	if err = databaseFile(db, false); err != nil {
		return err
	}

	store, err := data.NewStore(ctx, db)
	if err != nil {
		return ErrStartup
	}

	defer func() {
		if err := store.Close(); err != nil {
			result = ErrService
		}
	}()

	if err = fn(store); err != nil {
		return ErrService
	}
	return nil
}
