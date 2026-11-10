package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/models"
)

func TestStoreShouldPreserveTrackingWhenReopeningInitializedDatabase(t *testing.T) {
	// Given
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	initial := store.New(db)
	id := "0123456789abcdef0123456789abcdef"
	checked := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	stage := "B"
	if err = initial.Enrollments.Create(ctx, 101, id, "synthetic-number", "synthetic-password", stage, checked); err != nil {
		t.Fatal(err)
	}
	if err := initial.History.Append(ctx, id, "A", stage, checked); err != nil {
		t.Fatal(err)
	}
	guard := models.Cooldown{SubjectType: "manual_check", SubjectID: "101", StartedAt: checked}
	if err = initial.Cooldowns.Upsert(ctx, guard); err != nil {
		t.Fatal(err)
	}
	caseGuard := models.Cooldown{SubjectType: "case_check", SubjectID: id, StartedAt: checked}
	if err := initial.Cooldowns.Upsert(ctx, caseGuard); err != nil {
		t.Fatal(err)
	}
	// When
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	storage := store.New(db)
	enrollment, readErr := storage.Enrollments.FindByOwner(ctx, 101)
	history, historyErr := storage.History.FindByEnrollment(ctx, 101, id)
	cooldown, guardErr := storage.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, guard.SubjectType, guard.SubjectID)
	caseCooldown, caseGuardErr := storage.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, caseGuard.SubjectType, caseGuard.SubjectID)
	var version int
	var dirty bool
	versionErr := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	var obsolete int
	schemaErr := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name IN ('pending','recipients','pending_recipient')").Scan(&obsolete)
	// Then
	if readErr != nil || historyErr != nil || guardErr != nil || caseGuardErr != nil || versionErr != nil || version != 1 || dirty || schemaErr != nil || enrollment.ID != id || enrollment.Number != "synthetic-number" || enrollment.Password != "synthetic-password" || enrollment.Stage != stage || !enrollment.CheckedAt.Equal(checked) || len(history) != 1 || history[0].Stage != "B" || !cooldown.StartedAt.Equal(checked) || !caseCooldown.StartedAt.Equal(checked) || obsolete != 0 {
		t.Fatalf("reopening lost tracking: enrollment=%+v history=%+v guard=%+v caseGuard=%+v version=%d dirty=%t obsolete=%d errors=%v/%v/%v/%v/%v/%v", enrollment, history, cooldown, caseCooldown, version, dirty, obsolete, readErr, historyErr, guardErr, caseGuardErr, versionErr, schemaErr)
	}
}
