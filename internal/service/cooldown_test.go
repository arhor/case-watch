package service

import (
	"errors"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/tracking"
)

func TestCooldownShouldPersistEntitiesIndependentlyWhenTypesShareSubject(t *testing.T) {
	// Given
	s, path, _, _ := persistenceFixture(t)
	c := data.Cooldown{SubjectType: "future_operation", SubjectID: "101", StartedAt: epoch}
	other := data.Cooldown{SubjectType: "manual_check", SubjectID: c.SubjectID, StartedAt: epoch.Add(time.Minute)}
	for _, row := range []data.Cooldown{c, other} {
		if err := s.Cooldowns.Upsert(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	// When
	c.StartedAt = time.Unix(0, 0).UTC()
	if err := s.Cooldowns.Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openPersistenceDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// Then
	for _, want := range []data.Cooldown{c, other} {
		got, err := reopened.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, want.SubjectType, want.SubjectID)
		if err != nil || got.SubjectType != want.SubjectType || got.SubjectID != want.SubjectID || !got.StartedAt.Equal(want.StartedAt) {
			t.Fatalf("got %+v, want %+v: %v", got, want, err)
		}
	}
	for range 2 {
		if err := reopened.Cooldowns.DeleteBySubjectTypeAndSubjectID(ctx, c.SubjectType, c.SubjectID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reopened.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, c.SubjectType, c.SubjectID); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := reopened.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, other.SubjectType, other.SubjectID); err != nil {
		t.Fatal(err)
	}
}

func TestCooldownShouldRejectInvalidEntitiesWhenKeysOrTimeAreMissing(t *testing.T) {
	// Given
	s, _, _, _ := persistenceFixture(t)
	for _, c := range []data.Cooldown{
		{SubjectType: " \t\n", SubjectID: "101", StartedAt: epoch},
		{SubjectType: "manual_check", SubjectID: "\n\t", StartedAt: epoch},
		{SubjectType: "manual_check", SubjectID: "101"},
	} {
		// When
		err := s.Cooldowns.Upsert(ctx, c)
		// Then
		if !errors.Is(err, data.ErrInvalid) {
			t.Fatalf("upsert: %v", err)
		}
		if c.StartedAt.IsZero() {
			continue
		}
		if _, err := s.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, c.SubjectType, c.SubjectID); !errors.Is(err, data.ErrInvalid) {
			t.Fatalf("find: %v", err)
		}
		if err := s.Cooldowns.DeleteBySubjectTypeAndSubjectID(ctx, c.SubjectType, c.SubjectID); !errors.Is(err, data.ErrInvalid) {
			t.Fatalf("delete: %v", err)
		}
	}
}

func TestReserveCheckShouldLeaveCooldownsUntouchedWhenIdentityIsInvalid(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	p := tracking.Persistence{DB: s.Store}
	// When / Then
	for _, owner := range []int64{202, 0} {
		if _, err := p.ReserveCheck(ctx, owner, c.ID, true, epoch); err == nil {
			t.Fatal("invalid identity reserved")
		}
	}
	if count(t, s, "cooldowns") != 0 {
		t.Fatal("invalid identities wrote cooldowns")
	}
	if _, err := p.ReserveCheck(ctx, 101, c.ID, true, epoch); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(ctx, 101, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, "case_check", c.ID); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("case cleanup: %v", err)
	}
	if _, err := s.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, "manual_check", "101"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReserveCheck(ctx, 101, c.ID, true, epoch.Add(time.Minute)); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("removed identity: %v", err)
	}
	if count(t, s, "cooldowns") != 1 {
		t.Fatal("removed identity recreated cooldown")
	}
}
