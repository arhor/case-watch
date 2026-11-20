package repositories_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestNewStore(test *testing.T) {
	test.Run("should panic when database is missing", func(test *testing.T) {
		// Given
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			repositories.NewStore(nil)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: DB", recovered)
	})
}

func TestStoreRunInTx(test *testing.T) {
	test.Run("should commit when callback succeeds", func(test *testing.T) {
		// Given
		ctx := context.Background()
		db, err := database.Open(ctx, filepath.Join(test.TempDir(), "state.db"))
		assert.NoError(test, err)
		test.Cleanup(func() { _ = db.Close() })
		store := repositories.NewStore(db)
		id := "0123456789abcdef0123456789abcdef"
		checkedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

		// When
		err = store.RunInTx(ctx, func(store models.Store) error {
			return store.TrackedCases().Create(ctx, 101, id, "synthetic-number", "synthetic-password", "A", checkedAt)
		})

		// Then
		assert.NoError(test, err)
		trackedCase, readError := store.TrackedCases().FindByOwner(ctx, 101)
		assert.NoError(test, readError, "committed record should be visible")
		assert.Equal(test, id, trackedCase.ID)
	})

	test.Run("should rollback when callback fails", func(test *testing.T) {
		// Given
		ctx := context.Background()
		db, err := database.Open(ctx, filepath.Join(test.TempDir(), "state.db"))
		assert.NoError(test, err)
		test.Cleanup(func() { _ = db.Close() })
		store := repositories.NewStore(db)
		id := "0123456789abcdef0123456789abcdef"
		checkedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
		targetError := errors.New("abort transaction")

		// When
		err = store.RunInTx(ctx, func(store models.Store) error {
			if err := store.TrackedCases().Create(ctx, 101, id, "synthetic-number", "synthetic-password", "A", checkedAt); err != nil {
				return err
			}
			return targetError
		})

		// Then
		assert.ErrorIs(test, err, targetError)
		_, readError := store.TrackedCases().FindByOwner(ctx, 101)
		assert.ErrorIs(test, readError, repositories.ErrNotFound, "rolled-back record should be absent")
	})

	test.Run("should reject nested transaction when already in transaction", func(test *testing.T) {
		// Given
		ctx := context.Background()
		db, err := database.Open(ctx, filepath.Join(test.TempDir(), "state.db"))
		assert.NoError(test, err)
		defer db.Close()
		store := repositories.NewStore(db)

		// When
		err = store.RunInTx(ctx, func(store models.Store) error {
			return store.RunInTx(ctx, func(models.Store) error { return nil })
		})

		// Then
		assert.ErrorIs(test, err, repositories.ErrTransaction)
	})
}
