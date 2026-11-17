package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/models"
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
			err = store.Enrollments().Create(ctx, 101, id, "synthetic-number", "synthetic-password", stage, checked)
			assert.NoError(test, err)
		}
		{
			err := store.History().Append(ctx, id, "A", stage, checked)
			assert.NoError(test, err)
		}
		guard := models.Cooldown{SubjectType: "manual_check", SubjectID: "101", StartedAt: checked}
		{
			err = store.Cooldowns().Upsert(ctx, guard)
			assert.NoError(test, err)
		}
		caseGuard := models.Cooldown{SubjectType: "case_check", SubjectID: id, StartedAt: checked}
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
		enrollment, readErr := store.Enrollments().FindByOwner(ctx, 101)
		history, historyErr := store.History().FindByEnrollment(ctx, 101, id)
		cooldown, guardErr := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, guard.SubjectType, guard.SubjectID)
		caseCooldown, caseGuardErr := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, caseGuard.SubjectType, caseGuard.SubjectID)
		var version int
		var dirty bool
		versionErr := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
		var obsolete int
		schemaErr := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name IN ('pending','recipients','pending_recipient')").Scan(&obsolete)

		// Then
		assert.NoError(test, readErr, "reopening lost tracking")
		assert.NoError(test, historyErr, "reopening lost tracking")
		assert.NoError(test, guardErr, "reopening lost tracking")
		assert.NoError(test, caseGuardErr, "reopening lost tracking")
		assert.NoError(test, versionErr, "reopening lost tracking")
		assert.Equal(test, 1, version, "reopening lost tracking")
		assert.True(test, !dirty, "reopening lost tracking")
		assert.NoError(test, schemaErr, "reopening lost tracking")
		assert.Equal(test, id, enrollment.ID, "reopening lost tracking")
		assert.Equal(test, "synthetic-number", enrollment.Number, "reopening lost tracking")
		assert.Equal(test, "synthetic-password", enrollment.Password, "reopening lost tracking")
		assert.Equal(test, stage, enrollment.Stage, "reopening lost tracking")
		assert.True(test, enrollment.CheckedAt.Equal(checked), "reopening lost tracking")
		assert.Equal(test, 1, len(history), "reopening lost tracking")
		assert.Equal(test, "B", history[0].Stage, "reopening lost tracking")
		assert.True(test, cooldown.StartedAt.Equal(checked), "reopening lost tracking")
		assert.True(test, caseCooldown.StartedAt.Equal(checked), "reopening lost tracking")
		assert.Equal(test, 0, obsolete, "reopening lost tracking")
	})
}
