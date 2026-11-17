package repositories_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/repositories"
)

func TestRunInTxShouldCommitAndRollbackWhenCallbacksSucceedOrFail(t *testing.T) {
	// Given
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repositories.NewStore(db)
	id := "0123456789abcdef0123456789abcdef"
	number, password := "synthetic-number", "synthetic-password"
	stage := "A"
	checkedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	abort := errors.New("abort transaction")
	// When
	err = store.RunInTx(ctx, func(store *repositories.Store) error {
		if err := store.Enrollments.Create(ctx, 101, id, number, password, stage, checkedAt); err != nil {
			return err
		}
		return abort
	})
	// Then
	if !errors.Is(err, abort) {
		t.Fatalf("rollback returned %v, want %v", err, abort)
	}
	if _, err := store.Enrollments.FindByOwner(ctx, 101); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("rolled-back record was visible: %v", err)
	}
	if err := store.RunInTx(ctx, func(store *repositories.Store) error {
		return store.Enrollments.Create(ctx, 101, id, number, password, stage, checkedAt)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enrollments.FindByOwner(ctx, 101); err != nil {
		t.Fatalf("committed record was missing: %v", err)
	}
}

func TestRunInTxShouldRejectNestedTransaction(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repositories.NewStore(db)
	err = store.RunInTx(ctx, func(store *repositories.Store) error {
		return store.RunInTx(ctx, func(*repositories.Store) error { return nil })
	})
	if !errors.Is(err, repositories.ErrTransaction) {
		t.Fatalf("nested transaction returned %v, want %v", err, repositories.ErrTransaction)
	}
}
