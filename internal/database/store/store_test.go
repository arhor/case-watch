package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/repositories"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/domain"
)

func TestRunInTxShouldCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	storage := store.New(db)
	id := "0123456789abcdef0123456789abcdef"
	credentials := domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}
	observation := domain.Observation{Stage: "A", CheckedAt: time.Now().UTC()}
	abort := errors.New("abort transaction")
	err = storage.RunInTx(ctx, func(tx *store.Store) error {
		if err := tx.Enrollments.Create(ctx, 101, id, credentials, observation); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("rollback returned %v, want %v", err, abort)
	}
	if _, err := storage.Enrollments.FindByOwner(ctx, 101); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("rolled-back record was visible: %v", err)
	}
	if err := storage.RunInTx(ctx, func(tx *store.Store) error {
		return tx.Enrollments.Create(ctx, 101, id, credentials, observation)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Enrollments.FindByOwner(ctx, 101); err != nil {
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
	storage := store.New(db)
	err = storage.RunInTx(ctx, func(tx *store.Store) error {
		return tx.RunInTx(ctx, func(*store.Store) error { return nil })
	})
	if !errors.Is(err, store.ErrTransaction) {
		t.Fatalf("nested transaction returned %v, want %v", err, store.ErrTransaction)
	}
}
