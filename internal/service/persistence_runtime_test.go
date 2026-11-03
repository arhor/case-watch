package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/tracking"
)

func TestReserveCheckShouldPersistSixtySecondGuardsWhenAttemptsFail(t *testing.T) {
	// Given
	s, path, _, c := persistenceFixture(t)
	enroll(t, s, c)
	// When
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := (tracking.Persistence{DB: s.Store}).ReserveCheck(ctx, 101, c.ID, true, epoch)
			if e != nil {
				t.Error(e)
			}
			if r.Reserved {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	s.Close()
	reopened, e := openPersistenceDatabase(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	before, e1 := (tracking.Persistence{DB: reopened.Store}).ReserveCheck(ctx, 101, c.ID, true, epoch.Add(59*time.Second))
	at, e2 := (tracking.Persistence{DB: reopened.Store}).ReserveCheck(ctx, 101, c.ID, true, epoch.Add(time.Minute))
	// Then
	if wins.Load() != 1 || e1 != nil || e2 != nil || before.Reserved || !at.Reserved {
		t.Fatalf("guards: wins=%d before=%+v at=%+v errors=%v/%v", wins.Load(), before, at, e1, e2)
	}
}

func TestDeliveryShouldRetainOrderAndBudgetWhenRecoveredAfterCrash(t *testing.T) {
	// Given
	s, path, _, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("A", 2))
	// When
	first, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch)
	if e != nil {
		t.Fatal(e)
	}
	_, busy := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch)
	s.Close()
	s, e = openPersistenceDatabase(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	recovery := (notifications.Persistence{DB: s.Store}).RecoverDeliveries(ctx, epoch)
	_, early := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch.Add(29*time.Second))
	retry, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch.Add(30*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	stale := (notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, first, application.SendOutcome{Category: application.SendAccepted, MessageID: 1}, epoch)
	accepted := (notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, retry, application.SendOutcome{Category: application.SendAccepted, MessageID: 2}, epoch.Add(31*time.Second))
	next, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch.Add(31*time.Second))
	// Then
	if !errors.Is(busy, data.ErrNotEligible) || recovery != nil || !errors.Is(early, data.ErrNotEligible) || retry.Sequence != first.Sequence || retry.CycleAttempts != 2 || retry.LifetimeAttempts != 2 || !errors.Is(stale, data.ErrNotFound) || accepted != nil || e != nil || next.Sequence <= first.Sequence {
		t.Fatalf("ordered recovery: first=%+v retry=%+v next=%+v busy=%v recovery=%v early=%v stale=%v accepted=%v nextError=%v", first, retry, next, busy, recovery, early, stale, accepted, e)
	}
}

func TestDeliveryShouldResumeWhenExplicitUnblockFollowsExhaustion(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	now := epoch
	// When
	for i := 0; i < 6; i++ {
		d, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, now)
		if e != nil {
			t.Fatal(e)
		}
		if e = (notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, d, application.SendOutcome{Category: application.SendTransient}, now); e != nil {
			t.Fatal(e)
		}
		now = now.Add(30 * time.Second * time.Duration(1<<uint(i)))
	}
	r, _, e := (notifications.Persistence{DB: s.Store}).DeliveryGates(ctx, 101)
	if e != nil {
		t.Fatal(e)
	}
	(notifications.Persistence{DB: s.Store}).SetBlocked(ctx, 101, false)
	_, paused := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, now)
	resume := (notifications.Persistence{DB: s.Store}).ResumeUnblocked(ctx, 101)
	d, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, now)
	// Then
	if r.Pause != "exhausted" || !errors.Is(paused, data.ErrNotEligible) || resume != nil || e != nil || d.CycleAttempts != 1 || d.LifetimeAttempts != 7 {
		t.Fatalf("resume: state=%+v paused=%v resume=%v delivery=%+v error=%v", r, paused, resume, d, e)
	}
}

func TestRemoveShouldRetainGuardsAndFenceLateResultsWhenReenrolled(t *testing.T) {
	// Given
	s, _, k, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).ReserveCheck(ctx, 101, c.ID, true, epoch)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	d, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch)
	if e != nil {
		t.Fatal(e)
	}
	(notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, d, application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour}, epoch)
	(notifications.Persistence{DB: s.Store}).SetBlocked(ctx, 101, true)
	// When
	removed := (tracking.Persistence{DB: s.Store}).Remove(ctx, 101, c.ID)
	reuse := (tracking.Persistence{DB: s.Store}).Enroll(ctx, 101, c, observation("A", 0))
	id, _ := data.NewEnrollmentID()
	fresh, e := k.Seal(id, application.Credentials{Number: "synthetic", Password: "synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	if e = (tracking.Persistence{DB: s.Store}).Enroll(ctx, 101, fresh, observation("C", 0)); e != nil {
		t.Fatal(e)
	}
	late := (tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("D", 2))
	ack := (notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, d, application.SendOutcome{Category: application.SendAccepted, MessageID: 3}, epoch)
	guard, e := (tracking.Persistence{DB: s.Store}).ReserveCheck(ctx, 101, id, true, epoch.Add(59*time.Second))
	r, g, gateErr := (notifications.Persistence{DB: s.Store}).DeliveryGates(ctx, 101)
	// Then
	if removed != nil || !errors.Is(reuse, data.ErrExists) || !errors.Is(late, data.ErrNotFound) || !errors.Is(ack, data.ErrNotFound) || e != nil || guard.Reserved || gateErr != nil || !r.Blocked || !g.RateUntil.Equal(epoch.Add(time.Hour)) || count(t, s, "history") != 0 || count(t, s, "pending") != 0 || count(t, s, "enrollments") != 2 {
		t.Fatalf("removal guards/fence removed=%v reuse=%v late=%v ack=%v guard=%+v error=%v recipient=%+v global=%+v", removed, reuse, late, ack, guard, e, r, g)
	}
}

func TestDeliveryShouldHonorGlobalRateDeadlineWhenRecipientResumes(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	d, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch)
	if e != nil {
		t.Fatal(e)
	}
	// When
	completed := (notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, d, application.SendOutcome{Category: application.SendRateLimited, RetryAfter: 2 * time.Minute}, epoch)
	(notifications.Persistence{DB: s.Store}).SetBlocked(ctx, 101, true)
	(notifications.Persistence{DB: s.Store}).SetBlocked(ctx, 101, false)
	_, early := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch.Add(119*time.Second))
	at, e := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch.Add(2*time.Minute))
	// Then
	if completed != nil || !errors.Is(early, data.ErrNotEligible) || e != nil || at.CycleAttempts != 1 || at.LifetimeAttempts != 2 {
		t.Fatalf("rate gate completed=%v early=%v at=%+v error=%v", completed, early, at, e)
	}
}

func TestRemoveShouldRollbackAllRowsWhenDeletionFails(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	if _, e := s.db.Exec(`CREATE TRIGGER fail_remove BEFORE DELETE ON pending BEGIN SELECT RAISE(ABORT,'synthetic'); END;`); e != nil {
		t.Fatal(e)
	}
	// When
	err := (tracking.Persistence{DB: s.Store}).Remove(ctx, 101, c.ID)
	current, readErr := s.Enrollments.FindByOwner(ctx, 101)
	// Then
	if !errors.Is(err, data.ErrStorage) || readErr != nil || current.Credentials.ID != c.ID || count(t, s, "history") != 1 || count(t, s, "pending") != 1 {
		t.Fatalf("partial removal err=%v read=%v", err, readErr)
	}
}

func TestReserveDeliveryShouldAllowOneAttemptWhenCallersRace(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	var wins atomic.Int32
	var wg sync.WaitGroup
	// When
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, data.ErrNotEligible) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	queue, err := s.Pending.FindByRecipient(ctx, 101)
	// Then
	if err != nil || wins.Load() != 1 || len(queue) != 1 || queue[0].LifetimeAttempts != 1 {
		t.Fatalf("reservations=%d queue=%+v error=%v", wins.Load(), queue, err)
	}
}

func TestReserveCheckShouldRollbackCaseGuardWhenManualWriteFails(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_guard BEFORE INSERT ON manual_guards BEGIN SELECT RAISE(ABORT,'synthetic'); END;`); err != nil {
		t.Fatal(err)
	}
	// When
	_, failed := (tracking.Persistence{DB: s.Store}).ReserveCheck(ctx, 101, c.ID, true, epoch)
	scheduled, err := (tracking.Persistence{DB: s.Store}).ReserveCheck(ctx, 101, c.ID, false, epoch)
	// Then
	if !errors.Is(failed, data.ErrStorage) || err != nil || !scheduled.Reserved {
		t.Fatalf("partial reservation failed=%v scheduled=%+v error=%v", failed, scheduled, err)
	}
}

func TestCompleteDeliveryShouldRollbackGateWhenOutcomeWriteFails(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	(tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	d, err := (notifications.Persistence{DB: s.Store}).ReserveDelivery(ctx, 101, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_outcome BEFORE UPDATE ON pending BEGIN SELECT RAISE(ABORT,'synthetic'); END;`); err != nil {
		t.Fatal(err)
	}
	// When
	failed := (notifications.Persistence{DB: s.Store}).CompleteDelivery(ctx, d, application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour}, epoch)
	_, g, err := (notifications.Persistence{DB: s.Store}).DeliveryGates(ctx, 101)
	q, queueErr := s.Pending.FindByRecipient(ctx, 101)
	// Then
	if !errors.Is(failed, data.ErrStorage) || err != nil || queueErr != nil || !g.RateUntil.IsZero() || q[0].Status != "reserved" || q[0].LifetimeAttempts != 1 {
		t.Fatalf("partial outcome failed=%v gates=%+v queue=%+v", failed, g, q)
	}
}

func TestRemoveShouldHideAndScrubEnrollmentWhenReopenedAfterSoftDeletion(t *testing.T) {
	// Given
	s, path, _, c := persistenceFixture(t)
	enroll(t, s, c)
	// When
	removeErr := (tracking.Persistence{DB: s.Store}).Remove(ctx, 101, c.ID)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openPersistenceDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	_, readErr := reopened.Enrollments.FindByOwner(ctx, 101)
	all, listErr := reopened.Enrollments.FindAll(ctx)
	_, reserveErr := (tracking.Persistence{DB: reopened.Store}).ReserveCheck(ctx, 101, c.ID, true, epoch)
	_, eligibilityErr := (tracking.Persistence{DB: reopened.Store}).CheckEligibility(ctx, 101, c.ID, true)
	repeatErr := (tracking.Persistence{DB: reopened.Store}).Remove(ctx, 101, c.ID)
	reuseErr := (tracking.Persistence{DB: reopened.Store}).Enroll(ctx, 202, c, observation("A", 0))
	rewrites := 0
	rewriteErr := RewriteCredentials(ctx, reopened.Store, func(record application.SealedCredentials) (application.SealedCredentials, error) {
		rewrites++
		return record, nil
	})
	// Then
	if removeErr != nil || !errors.Is(readErr, data.ErrNotFound) || listErr != nil || len(all) != 0 ||
		!errors.Is(reserveErr, data.ErrNotFound) || !errors.Is(eligibilityErr, data.ErrNotFound) ||
		!errors.Is(repeatErr, data.ErrNotFound) || !errors.Is(reuseErr, data.ErrExists) || rewriteErr != nil || rewrites != 0 {
		t.Fatalf("deleted enrollment visible: remove=%v read=%v list=%v reserve=%v eligibility=%v repeat=%v reuse=%v rewrite=%v calls=%d", removeErr, readErr, listErr, reserveErr, eligibilityErr, repeatErr, reuseErr, rewriteErr, rewrites)
	}
	var tombstones int
	err = reopened.db.QueryRow(`SELECT count(*) FROM enrollments WHERE id=? AND owner_id=101
 AND deleted_at IS NOT NULL AND credential_version IS NULL AND key_id IS NULL
 AND payload IS NULL AND stage IS NULL AND checked_at IS NULL`, string(c.ID)).Scan(&tombstones)
	if err != nil || tombstones != 1 {
		t.Fatalf("missing scrubbed soft-deleted enrollment: count=%d error=%v", tombstones, err)
	}
}
