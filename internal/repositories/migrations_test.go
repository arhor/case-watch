package repositories

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

func TestStoreShouldPreserveTrackingWhenMigratingNotificationQueue(t *testing.T) {
	// Given
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initial, err := os.ReadFile("migrations/000001_initial.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations (version uint64, dirty bool); INSERT INTO schema_migrations VALUES (1,false)"); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	checked := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	observation := domain.Observation{Stage: "B", CheckedAt: checked}
	if err = (&EnrollmentRepository{db: db}).Create(ctx, 101, id, domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}, observation); err != nil {
		t.Fatal(err)
	}
	if err := (&HistoryRepository{db: db}).Append(ctx, id, "A", observation); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO pending (history_sequence,recipient_id,status,cycle_attempts,lifetime_attempts) SELECT sequence,101,'reserved',1,1 FROM history WHERE enrollment_id = ?", id); err != nil {
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
	if readErr != nil || historyErr != nil || guardErr != nil || caseGuardErr != nil || versionErr != nil || version != 2 || dirty || schemaErr != nil || enrollment.ID != id || enrollment.Credentials != (domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}) || enrollment.Observation != observation || len(history) != 1 || history[0].Stage != "B" || !cooldown.StartedAt.Equal(checked) || !caseCooldown.StartedAt.Equal(checked) || obsolete != 0 {
		t.Fatalf("migration lost tracking: enrollment=%+v history=%+v guard=%+v caseGuard=%+v version=%d dirty=%t obsolete=%d errors=%v/%v/%v/%v/%v/%v", enrollment, history, cooldown, caseCooldown, version, dirty, obsolete, readErr, historyErr, guardErr, caseGuardErr, versionErr, schemaErr)
	}
}
