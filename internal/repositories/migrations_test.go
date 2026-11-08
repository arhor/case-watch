package repositories

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

func TestStoreShouldPreserveTrackingWhenReopeningInitializedDatabase(t *testing.T) {
	// Given
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	initial, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer initial.Close()
	db := initial.db
	id := "0123456789abcdef0123456789abcdef"
	checked := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	observation := domain.Observation{Stage: "B", CheckedAt: checked}
	if err = (&EnrollmentRepository{db: db}).Create(ctx, 101, id, domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}, observation); err != nil {
		t.Fatal(err)
	}
	if err := (&HistoryRepository{db: db}).Append(ctx, id, "A", observation); err != nil {
		t.Fatal(err)
	}
	guard := Cooldown{SubjectType: "manual_check", SubjectID: "101", StartedAt: checked}
	if err = (&CooldownRepository{db: db}).Upsert(ctx, guard); err != nil {
		t.Fatal(err)
	}
	caseGuard := Cooldown{SubjectType: "case_check", SubjectID: id, StartedAt: checked}
	if err := (&CooldownRepository{db: db}).Upsert(ctx, caseGuard); err != nil {
		t.Fatal(err)
	}
	// When
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	store, openErr := NewStore(ctx, path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer store.Close()
	enrollment, readErr := store.Enrollments.FindByOwner(ctx, 101)
	history, historyErr := store.History.FindByEnrollment(ctx, 101, id)
	cooldown, guardErr := store.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, guard.SubjectType, guard.SubjectID)
	caseCooldown, caseGuardErr := store.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, caseGuard.SubjectType, caseGuard.SubjectID)
	var version int
	var dirty bool
	versionErr := store.db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	var obsolete int
	schemaErr := store.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name IN ('pending','recipients','pending_recipient')").Scan(&obsolete)
	// Then
	if readErr != nil || historyErr != nil || guardErr != nil || caseGuardErr != nil || versionErr != nil || version != 1 || dirty || schemaErr != nil || enrollment.ID != id || enrollment.Credentials != (domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}) || enrollment.Observation != observation || len(history) != 1 || history[0].Stage != "B" || !cooldown.StartedAt.Equal(checked) || !caseCooldown.StartedAt.Equal(checked) || obsolete != 0 {
		t.Fatalf("reopening lost tracking: enrollment=%+v history=%+v guard=%+v caseGuard=%+v version=%d dirty=%t obsolete=%d errors=%v/%v/%v/%v/%v/%v", enrollment, history, cooldown, caseCooldown, version, dirty, obsolete, readErr, historyErr, guardErr, caseGuardErr, versionErr, schemaErr)
	}
}
