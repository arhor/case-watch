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

func TestRunInTxShouldCommitAndRollbackWhenCallbacksSucceedOrFail(t *testing.T) {
	// Given
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	assert.NoError(t, err)
	defer db.Close()
	store := repositories.NewStore(db)
	id := "0123456789abcdef0123456789abcdef"
	number, password := "synthetic-number", "synthetic-password"
	stage := "A"
	checkedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	abort := errors.New("abort transaction")
	// When
	err = store.RunInTx(ctx, func(store models.Store) error {
		if err := store.Enrollments().Create(ctx, 101, id, number, password, stage, checkedAt); err != nil {
			return err
		}
		return abort
	})
	// Then
	assert.ErrorIs(t, err, abort)
	{
		_, err := store.Enrollments().FindByOwner(ctx, 101)
		assert.ErrorIs(t, err, repositories.ErrNotFound, "rolled-back record was visible")
	}
	{
		err := store.RunInTx(ctx, func(store models.Store) error {
			return store.Enrollments().Create(ctx, 101, id, number, password, stage, checkedAt)
		})
		assert.NoError(t, err)
	}
	{
		_, err := store.Enrollments().FindByOwner(ctx, 101)
		assert.NoError(t, err, "committed record was missing")
	}
}

func TestRunInTxShouldRejectNestedTransactionWhenAlreadyInTransaction(t *testing.T) {
	// Given
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	assert.NoError(t, err)
	defer db.Close()
	store := repositories.NewStore(db)
	// When
	err = store.RunInTx(ctx, func(store models.Store) error {
		return store.RunInTx(ctx, func(models.Store) error { return nil })
	})
	// Then
	assert.ErrorIs(t, err, repositories.ErrTransaction)
}
