package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestOpen(test *testing.T) {
	test.Run("should preserve tracking when reopening initialized database", func(test *testing.T) {
		// Given
		ctx := context.Background()
		path := filepath.Join(test.TempDir(), "state.db")
		db, err := database.Open(ctx, path)
		assert.NoError(test, err)
		store := repositories.NewStore(db)
		id := "0123456789abcdef0123456789abcdef"
		checked := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
		stage := "B"
		{
			err = store.TrackedCases().Create(ctx, 101, id, "synthetic-number", "synthetic-password", domain.PortalSnapshot{Stage: stage, CheckedAt: checked})
			assert.NoError(test, err)
		}
		{
			err := store.TrackedCasesHistory().Append(ctx, id, "A", stage, checked)
			assert.NoError(test, err)
		}
		guard := domain.Cooldown{SubjectType: "manual_check", SubjectID: "101", StartedAt: checked}
		{
			err = store.Cooldowns().Upsert(ctx, guard)
			assert.NoError(test, err)
		}
		caseGuard := domain.Cooldown{SubjectType: "case_check", SubjectID: id, StartedAt: checked}
		{
			err := store.Cooldowns().Upsert(ctx, caseGuard)
			assert.NoError(test, err)
		}

		// When
		{
			err := db.Close()
			assert.NoError(test, err)
		}
		db, err = database.Open(ctx, path)
		assert.NoError(test, err)
		defer db.Close()
		store = repositories.NewStore(db)
		trackedCase, readErr := store.TrackedCases().FindByOwner(ctx, 101)
		history, historyErr := store.TrackedCasesHistory().FindByTrackedCase(ctx, 101, id)
		cooldown, guardErr := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, guard.SubjectType, guard.SubjectID)
		caseCooldown, caseGuardErr := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, caseGuard.SubjectType, caseGuard.SubjectID)
		var version int
		var dirty bool
		versionErr := db.QueryRowContext(ctx, `
			SELECT version, dirty
			  FROM schema_migrations
		`).Scan(&version, &dirty)
		var obsolete int
		schemaErr := db.QueryRowContext(ctx, `
			SELECT count(*)
			  FROM sqlite_master
			 WHERE name IN ('pending', 'recipients', 'pending_recipient')
		`).Scan(&obsolete)

		// Then
		assert.NoError(test, readErr, "reopening lost tracking")
		assert.NoError(test, historyErr, "reopening lost tracking")
		assert.NoError(test, guardErr, "reopening lost tracking")
		assert.NoError(test, caseGuardErr, "reopening lost tracking")
		assert.NoError(test, versionErr, "reopening lost tracking")
		assert.Equal(test, 2, version, "reopening lost tracking")
		assert.True(test, !dirty, "reopening lost tracking")
		assert.NoError(test, schemaErr, "reopening lost tracking")
		assert.Equal(test, id, trackedCase.ID, "reopening lost tracking")
		assert.Equal(test, "synthetic-number", trackedCase.Number, "reopening lost tracking")
		assert.Equal(test, "synthetic-password", trackedCase.Password, "reopening lost tracking")
		assert.Equal(test, stage, trackedCase.Stage, "reopening lost tracking")
		assert.True(test, trackedCase.CheckedAt.Equal(checked), "reopening lost tracking")
		assert.Equal(test, 1, len(history), "reopening lost tracking")
		assert.Equal(test, "B", history[0].Stage, "reopening lost tracking")
		assert.True(test, cooldown.StartedAt.Equal(checked), "reopening lost tracking")
		assert.True(test, caseCooldown.StartedAt.Equal(checked), "reopening lost tracking")
		assert.Equal(test, 0, obsolete, "reopening lost tracking")
	})
}

func TestOpen_UpgradeFromVersionOne(test *testing.T) {
	// Given
	ctx := context.Background()
	path := filepath.Join(test.TempDir(), "version-one.db")
	db, err := database.Open(ctx, path)
	assert.NoError(test, err)
	store := repositories.NewStore(db)
	checkedAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	activeID := "0123456789abcdef0123456789abcdef"
	deletedID := "abcdef0123456789abcdef0123456789"
	assert.NoError(test, store.TrackedCases().Create(ctx, 101, activeID, "synthetic-active", "synthetic-password", domain.PortalSnapshot{Stage: "B", CheckedAt: checkedAt}))
	assert.NoError(test, store.TrackedCasesHistory().Append(ctx, activeID, "A", "B", checkedAt))
	assert.NoError(test, store.Cooldowns().Upsert(ctx, domain.Cooldown{SubjectType: "case_check", SubjectID: activeID, StartedAt: checkedAt}))
	assert.NoError(test, store.TrackedCases().Create(ctx, 102, deletedID, "synthetic-deleted", "synthetic-password", domain.PortalSnapshot{Stage: "C", CheckedAt: checkedAt}))
	assert.NoError(test, store.TrackedCasesHistory().Append(ctx, deletedID, "B", "C", checkedAt))
	assert.NoError(test, store.Cooldowns().Upsert(ctx, domain.Cooldown{SubjectType: "case_check", SubjectID: deletedID, StartedAt: checkedAt}))
	assert.NoError(test, store.TrackedCases().SoftDelete(ctx, 102, deletedID, checkedAt.Add(time.Second)))
	_, err = db.ExecContext(ctx, `ALTER TABLE tracked_cases DROP COLUMN curr_html`)
	assert.NoError(test, err)
	_, err = db.ExecContext(ctx, `UPDATE schema_migrations SET version = 1, dirty = 0`)
	assert.NoError(test, err)
	assert.NoError(test, db.Close())

	// When
	db, err = database.Open(ctx, path)
	assert.NoError(test, err)
	defer db.Close()
	store = repositories.NewStore(db)
	active, activeErr := store.TrackedCases().FindByOwner(ctx, 101)
	history, historyErr := store.TrackedCasesHistory().FindByTrackedCase(ctx, 101, activeID)
	cooldown, cooldownErr := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, "case_check", activeID)
	var migratedHTML sql.NullString
	queryErr := db.QueryRowContext(ctx, `SELECT curr_html FROM tracked_cases WHERE id = ?`, activeID).Scan(&migratedHTML)
	var version int
	var dirty bool
	versionErr := db.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	var deletedHTML sql.NullString
	deletedErr := db.QueryRowContext(ctx, `SELECT curr_html FROM tracked_cases WHERE id = ?`, deletedID).Scan(&deletedHTML)
	deletedHistory, deletedHistoryErr := store.TrackedCasesHistory().FindByTrackedCase(ctx, 102, deletedID)
	deletedCooldown, deletedCooldownErr := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, "case_check", deletedID)

	// Then
	assert.NoError(test, activeErr)
	assert.NoError(test, historyErr)
	assert.NoError(test, cooldownErr)
	assert.NoError(test, queryErr)
	assert.NoError(test, versionErr)
	assert.NoError(test, deletedErr)
	assert.NoError(test, deletedHistoryErr)
	assert.NoError(test, deletedCooldownErr)
	assert.Equal(test, 2, version)
	assert.True(test, !dirty)
	assert.Equal(test, "B", active.Stage)
	assert.True(test, active.CheckedAt.Equal(checkedAt))
	assert.Equal(test, "", active.HTML)
	assert.True(test, !migratedHTML.Valid)
	assert.Equal(test, 1, len(history))
	assert.True(test, cooldown.StartedAt.Equal(checkedAt))
	assert.True(test, !deletedHTML.Valid)
	assert.Equal(test, 0, len(deletedHistory))
	assert.True(test, deletedCooldown.StartedAt.Equal(checkedAt))
}
