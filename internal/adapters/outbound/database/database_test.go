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

func TestStoreShouldPreserveTrackingWhenReopeningInitializedDatabase(t *testing.T) {
	// Given
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := database.Open(ctx, path)
	assert.NoError(t, err)
	store := repositories.NewStore(db)
	id := "0123456789abcdef0123456789abcdef"
	checked := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	stage := "B"
	{
		err = store.Enrollments().Create(ctx, 101, id, "synthetic-number", "synthetic-password", stage, checked)
		assert.NoError(t, err)
	}
	{
		err := store.History().Append(ctx, id, "A", stage, checked)
		assert.NoError(t, err)
	}
	guard := models.Cooldown{SubjectType: "manual_check", SubjectID: "101", StartedAt: checked}
	{
		err = store.Cooldowns().Upsert(ctx, guard)
		assert.NoError(t, err)
	}
	caseGuard := models.Cooldown{SubjectType: "case_check", SubjectID: id, StartedAt: checked}
	{
		err := store.Cooldowns().Upsert(ctx, caseGuard)
		assert.NoError(t, err)
	}
	// When
	{
		err := db.Close()
		assert.NoError(t, err)
	}
	db, err = database.Open(ctx, path)
	assert.NoError(t, err)
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
	assert.NoError(t, readErr, "reopening lost tracking")
	assert.NoError(t, historyErr, "reopening lost tracking")
	assert.NoError(t, guardErr, "reopening lost tracking")
	assert.NoError(t, caseGuardErr, "reopening lost tracking")
	assert.NoError(t, versionErr, "reopening lost tracking")
	assert.Equal(t, 1, version, "reopening lost tracking")
	assert.True(t, !dirty, "reopening lost tracking")
	assert.NoError(t, schemaErr, "reopening lost tracking")
	assert.Equal(t, id, enrollment.ID, "reopening lost tracking")
	assert.Equal(t, "synthetic-number", enrollment.Number, "reopening lost tracking")
	assert.Equal(t, "synthetic-password", enrollment.Password, "reopening lost tracking")
	assert.Equal(t, stage, enrollment.Stage, "reopening lost tracking")
	assert.True(t, enrollment.CheckedAt.Equal(checked), "reopening lost tracking")
	assert.Equal(t, 1, len(history), "reopening lost tracking")
	assert.Equal(t, "B", history[0].Stage, "reopening lost tracking")
	assert.True(t, cooldown.StartedAt.Equal(checked), "reopening lost tracking")
	assert.True(t, caseCooldown.StartedAt.Equal(checked), "reopening lost tracking")
	assert.Equal(t, 0, obsolete, "reopening lost tracking")
}
