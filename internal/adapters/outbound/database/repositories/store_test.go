package repositories_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/domain"
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

func TestTrackedCaseRepository(test *testing.T) {
	test.Run("should replace the complete observation and clear absent HTML", func(test *testing.T) {
		// Given
		ctx := context.Background()
		db, err := database.Open(ctx, filepath.Join(test.TempDir(), "state.db"))
		assert.NoError(test, err)
		defer db.Close()
		store := repositories.NewStore(db)
		id := "0123456789abcdef0123456789abcdef"
		checkedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
		initial := domain.PortalSnapshot{Stage: "A", HTML: " <p>opaque & exact</p>\n", HTMLAvailable: true, CheckedAt: checkedAt}
		assert.NoError(test, store.TrackedCases().Create(ctx, 101, id, "synthetic-number", "synthetic-password", initial))

		// When
		byOwner, ownerErr := store.TrackedCases().FindByOwner(ctx, 101)
		byIdentity, identityErr := store.TrackedCases().FindByIdentity(ctx, 101, id)
		all, allErr := store.TrackedCases().FindAll(ctx)
		unavailable := domain.PortalSnapshot{Stage: "B", CheckedAt: checkedAt.Add(time.Minute)}
		updateErr := store.TrackedCases().UpdateObservation(ctx, 101, id, unavailable)
		retained, retainedErr := store.TrackedCases().FindByOwner(ctx, 101)
		updated := domain.PortalSnapshot{Stage: "A", HTMLAvailable: true, CheckedAt: unavailable.CheckedAt.Add(time.Minute)}
		clearErr := store.TrackedCases().UpdateObservation(ctx, 101, id, updated)
		cleared, clearedErr := store.TrackedCases().FindByOwner(ctx, 101)
		withHTML := domain.PortalSnapshot{Stage: "A", HTML: "<p>delete me</p>", HTMLAvailable: true, CheckedAt: updated.CheckedAt.Add(time.Minute)}
		replaceErr := store.TrackedCases().UpdateObservation(ctx, 101, id, withHTML)
		deleteErr := store.TrackedCases().SoftDelete(ctx, 101, id, withHTML.CheckedAt.Add(time.Minute))
		var deletedHTML sql.NullString
		rawReadErr := db.QueryRowContext(ctx, `SELECT curr_html FROM tracked_cases WHERE id = ?`, id).Scan(&deletedHTML)

		// Then
		assert.NoError(test, ownerErr)
		assert.NoError(test, identityErr)
		assert.NoError(test, allErr)
		assert.NoError(test, updateErr)
		assert.NoError(test, retainedErr)
		assert.NoError(test, clearErr)
		assert.NoError(test, clearedErr)
		assert.NoError(test, replaceErr)
		assert.NoError(test, deleteErr)
		assert.NoError(test, rawReadErr)
		assert.Equal(test, initial.HTML, byOwner.HTML)
		assert.Equal(test, initial.HTML, byIdentity.HTML)
		assert.Equal(test, initial.HTML, all[0].HTML)
		assert.Equal(test, initial.HTML, retained.HTML)
		assert.Equal(test, unavailable.Stage, retained.Stage)
		assert.Equal(test, "", cleared.HTML)
		assert.Equal(test, updated.Stage, cleared.Stage)
		assert.True(test, cleared.CheckedAt.Equal(updated.CheckedAt))
		assert.True(test, !deletedHTML.Valid, "soft deletion must clear stored HTML")
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
		err = store.RunInTx(ctx, func(store domain.Store) error {
			return store.TrackedCases().Create(ctx, 101, id, "synthetic-number", "synthetic-password", domain.PortalSnapshot{Stage: "A", CheckedAt: checkedAt})
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
		err = store.RunInTx(ctx, func(store domain.Store) error {
			if err := store.TrackedCases().Create(ctx, 101, id, "synthetic-number", "synthetic-password", domain.PortalSnapshot{Stage: "A", CheckedAt: checkedAt}); err != nil {
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
		err = store.RunInTx(ctx, func(store domain.Store) error {
			return store.RunInTx(ctx, func(domain.Store) error { return nil })
		})

		// Then
		assert.ErrorIs(test, err, repositories.ErrTransaction)
	})
}
