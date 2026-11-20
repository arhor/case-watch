package services

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/testing/assert"
	"github.com/arhor/case-watch/internal/testing/clock"
)

func TestNewCaseService(test *testing.T) {
	test.Run("should panic when store is missing", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		config := CaseServiceConfig{Portal: environment.service.portal, Clock: environment.clock}
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			NewCaseService(config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: Store", recovered)
	})

	test.Run("should panic when portal is missing", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		config := CaseServiceConfig{Store: environment.store, Clock: environment.clock}
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			NewCaseService(config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: Portal", recovered)
	})

	test.Run("should panic when clock is missing", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		config := CaseServiceConfig{Store: environment.store, Portal: environment.service.portal}
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			NewCaseService(config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: Clock", recovered)
	})
}

func TestCaseServiceStatus(test *testing.T) {
	test.Run("should use completion freshness without consuming guards when cache is recent", func(test *testing.T) {
		// Given
		environment := setup(test, func(context.Context, string, string) (string, time.Time, error) {
			return "A", epoch.Add(-time.Hour), nil
		})
		environment.trackCase(test, 101)
		environment.clock.Set(epoch.Add(59 * time.Second))

		// When
		recent, firstError := environment.service.Status(context.Background(), 101)
		environment.clock.Set(epoch.Add(time.Minute))
		at, secondError := environment.service.Status(context.Background(), 101)
		trackedCase, _ := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		deadline, thirdError := environment.service.checkEligibility(context.Background(), 101, trackedCase.ID, true)

		// Then
		assert.NoError(test, firstError, "only completed checks should refresh cached status")
		assert.NoError(test, secondError, "only completed checks should refresh cached status")
		assert.NoError(test, thirdError, "only completed checks should refresh cached status")
		assert.True(test, !recent.Fresh, "only completed checks should refresh cached status")
		assert.True(test, at.Fresh, "only completed checks should refresh cached status")
		assert.Equal(test, 2, environment.calls.Load(), "only completed checks should refresh cached status")
		assert.True(test, recent.NextEligibleAt.Equal(epoch.Add(time.Minute)), "only completed checks should refresh cached status")
		assert.True(test, deadline.Equal(epoch.Add(2*time.Minute)), "only completed checks should refresh cached status")
		verifyObservation(test, recent, "A", epoch)
		verifyObservation(test, at, "A", epoch.Add(time.Minute))
	})

	test.Run("should retain stale cache and guard when refresh fails across restart", func(test *testing.T) {
		// Given
		var fail atomic.Bool
		environment := setup(test, func(context.Context, string, string) (string, time.Time, error) {
			if fail.Load() {
				return "", time.Time{}, errors.New("secret remote details")
			}
			return "A", time.Time{}, nil
		})
		environment.trackCase(test, 101)
		fail.Store(true)
		environment.clock.Set(epoch.Add(time.Minute))

		// When
		failed, firstError := environment.service.Status(context.Background(), 101)
		environment.reopen(test)
		environment.service = NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
			environment.calls.Add(1)
			return "", time.Time{}, errors.New("secret")
		}), Clock: environment.clock})
		environment.clock.Set(epoch.Add(119 * time.Second))
		before, secondError := environment.service.Status(context.Background(), 101)
		environment.clock.Set(epoch.Add(2 * time.Minute))
		at, thirdError := environment.service.Status(context.Background(), 101)

		// Then
		assert.ErrorIs(test, firstError, ErrCheck, "failed checks should retain stale status and the retry guard")
		assert.NoError(test, secondError, "failed checks should retain stale status and the retry guard")
		assert.ErrorIs(test, thirdError, ErrCheck, "failed checks should retain stale status and the retry guard")
		assert.True(test, !failed.Fresh, "failed checks should retain stale status and the retry guard")
		assert.True(test, !before.Fresh, "failed checks should retain stale status and the retry guard")
		assert.True(test, !at.Fresh, "failed checks should retain stale status and the retry guard")
		assert.Equal(test, 3, environment.calls.Load(), "failed checks should retain stale status and the retry guard")
		assert.True(test, !strings.Contains(firstError.Error(), "secret"), "failed checks should retain stale status and the retry guard")
		assert.True(test, before.NextEligibleAt.Equal(epoch.Add(2*time.Minute)), "failed checks should retain stale status and the retry guard")
		verifyObservation(test, failed, "A", epoch)
		verifyObservation(test, before, "A", epoch)
		verifyObservation(test, at, "A", epoch)
	})

	test.Run("should share single flight and keep other waiters when one caller cancels", func(test *testing.T) {
		// Given
		entered, release := make(chan struct{}), make(chan struct{})
		var block atomic.Bool
		environment := setup(test, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
			if block.Load() {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return "", time.Time{}, ctx.Err()
				}
			}
			return "A", time.Time{}, nil
		})
		environment.trackCase(test, 101)
		block.Store(true)
		environment.clock.Set(epoch.Add(time.Minute))
		firstBase, cancel := context.WithCancel(context.Background())
		first := waitingContext(firstBase)
		second := waitingContext(context.Background())
		third := waitingContext(context.Background())
		firstResult, secondResult, thirdResult := make(chan answer, 1), make(chan answer, 1), make(chan answer, 1)

		// When
		go func() { status, err := environment.service.Status(first, 101); firstResult <- answer{status, err} }()
		<-entered
		<-first.entered
		go func() { status, err := environment.service.Scheduled(second, 101); secondResult <- answer{status, err} }()
		<-second.entered
		go func() { status, err := environment.service.Status(third, 101); thirdResult <- answer{status, err} }()
		<-third.entered
		cancel()
		canceled := <-firstResult
		environment.clock.Set(epoch.Add(130 * time.Second))
		close(release)
		joined1, joined2 := <-secondResult, <-thirdResult
		environment.clock.Set(epoch.Add(189 * time.Second))
		recent, err := environment.service.Status(context.Background(), 101)

		// Then
		assert.ErrorIs(test, canceled.err, context.Canceled, "joins")
		assert.NoError(test, joined1.err, "joins")
		assert.NoError(test, joined2.err, "joins")
		assert.NoError(test, err, "joins")
		assert.Equal(test, 2, environment.calls.Load(), "joins")
		assert.True(test, !recent.Fresh, "joins")
		assert.True(test, joined1.status.NextEligibleAt.Equal(epoch.Add(190*time.Second)), "joins")
		verifyObservation(test, joined1.status, "A", epoch.Add(130*time.Second))
		verifyObservation(test, joined2.status, "A", epoch.Add(130*time.Second))
	})

	test.Run("should deny work when owner is invalid", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		environment.trackCase(test, 101)

		// When
		_, err := environment.service.Status(context.Background(), 0)

		// Then
		assert.ErrorIs(test, err, ErrDenied)
		assert.Equal(test, 1, environment.calls.Load(), "invalid owner should not trigger portal work")
	})

	test.Run("should return not found when owner has no tracked case", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		environment.trackCase(test, 101)

		// When
		_, err := environment.service.Status(context.Background(), 202)

		// Then
		assert.ErrorIs(test, err, repositories.ErrNotFound, "owners should only access their own tracked case")
		assert.Equal(test, 1, environment.calls.Load())
	})

	test.Run("should cancel underlying flight when last waiter leaves", func(test *testing.T) {
		// Given
		var block atomic.Bool
		entered, canceled := make(chan struct{}), make(chan struct{})
		environment := setup(test, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
			if block.Load() {
				close(entered)
				<-ctx.Done()
				close(canceled)
				return "", time.Time{}, ctx.Err()
			}
			return "A", time.Time{}, nil
		})
		environment.trackCase(test, 101)
		environment.clock.Set(epoch.Add(time.Minute))
		block.Store(true)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)

		// When
		go func() { _, err := environment.service.Status(ctx, 101); result <- err }()
		<-entered
		cancel()
		err := <-result
		<-canceled
		removed := environment.service.Remove(context.Background(), 101)

		// Then
		assert.ErrorIs(test, err, context.Canceled, "cancellation failed to drain")
		assert.NoError(test, removed, "cancellation failed to drain")
	})

	test.Run("should preserve old cache when observation commit fails", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		id := environment.trackCase(test, 101)
		var calls atomic.Int32
		rejectInsert(test, environment.path, "tracked_cases_history")
		service := NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
			calls.Add(1)
			return "B", time.Time{}, nil
		}), Clock: environment.clock})
		environment.clock.Set(epoch.Add(time.Minute))

		// When
		status, err := service.Status(context.Background(), 101)
		trackedCase, readErr := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		history, historyErr := environment.store.TrackedCasesHistory().FindByTrackedCase(context.Background(), 101, id)

		// Then
		assert.ErrorIs(test, err, ErrStorage, "failed refresh")
		assert.NoError(test, readErr, "failed refresh")
		assert.NoError(test, historyErr, "failed refresh")
		assert.Equal(test, "A", trackedCase.Stage, "failed refresh")
		assert.True(test, trackedCase.CheckedAt.Equal(epoch), "failed refresh")
		assert.Equal(test, 0, len(history), "failed refresh")
		assert.True(test, !status.Fresh, "failed refresh")
		assert.Equal(test, 1, calls.Load(), "failed refresh")
		verifyObservation(test, status, "A", epoch)
	})

	test.Run("should create durable transitions from both sources when stages change", func(test *testing.T) {
		// Given
		var stage atomic.Value
		stage.Store("A")
		environment := setup(test, func(context.Context, string, string) (string, time.Time, error) {
			return stage.Load().(string), time.Time{}, nil
		})
		id := environment.trackCase(test, 101)

		// When
		environment.clock.Set(epoch.Add(time.Minute))
		stage.Store("B")
		manual, firstError := environment.service.Status(context.Background(), 101)
		environment.clock.Set(epoch.Add(2 * time.Minute))
		stage.Store("A")
		scheduled, secondError := environment.service.Scheduled(context.Background(), 101)
		history, thirdError := environment.store.TrackedCasesHistory().FindByTrackedCase(context.Background(), 101, id)

		// Then
		assert.NoError(test, firstError, "shared observation transaction")
		assert.NoError(test, secondError, "shared observation transaction")
		assert.NoError(test, thirdError, "shared observation transaction")
		assert.True(test, manual.Fresh, "shared observation transaction")
		assert.True(test, scheduled.Fresh, "shared observation transaction")
		assert.Equal(test, 2, len(history), "shared observation transaction")
		assert.Equal(test, "B", history[0].Stage, "shared observation transaction")
		assert.Equal(test, "A", history[1].Stage, "shared observation transaction")
	})
}

func TestCaseServiceScheduled(test *testing.T) {
	test.Run("should skip guarded case and preserve manual budget when run at slot", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		id := environment.trackCase(test, 101)
		environment.clock.Set(epoch.Add(time.Minute))

		// When
		scheduled, firstError := environment.service.Scheduled(context.Background(), 101)
		environment.clock.Set(epoch.Add(119 * time.Second))
		guarded, secondError := environment.service.Scheduled(context.Background(), 101)
		cached, thirdError := environment.service.Status(context.Background(), 101)
		environment.clock.Set(epoch.Add(2 * time.Minute))
		manual, fourthError := environment.service.Status(context.Background(), 101)
		history, historyError := environment.store.TrackedCasesHistory().FindByTrackedCase(context.Background(), 101, id)

		// Then
		assert.NoError(test, firstError, "scheduled checks should preserve the shared guard and manual budget")
		assert.NoError(test, secondError, "scheduled checks should preserve the shared guard and manual budget")
		assert.NoError(test, thirdError, "scheduled checks should preserve the shared guard and manual budget")
		assert.NoError(test, fourthError, "scheduled checks should preserve the shared guard and manual budget")
		assert.NoError(test, historyError, "scheduled checks should preserve the shared guard and manual budget")
		assert.Equal(test, 3, environment.calls.Load(), "scheduled checks should preserve the shared guard and manual budget")
		assert.True(test, scheduled.Fresh, "scheduled checks should preserve the shared guard and manual budget")
		assert.True(test, guarded.NextEligibleAt.Equal(epoch.Add(2*time.Minute)), "scheduled checks should preserve the shared guard and manual budget")
		assert.True(test, !cached.Fresh, "scheduled checks should preserve the shared guard and manual budget")
		assert.True(test, manual.Fresh, "scheduled checks should preserve the shared guard and manual budget")
		assert.Equal(test, 0, len(history), "scheduled checks should preserve the shared guard and manual budget")
	})

	test.Run("should prevent manual retry when scheduled attempt fails", func(test *testing.T) {
		// Given
		var fail atomic.Bool
		environment := setup(test, func(context.Context, string, string) (string, time.Time, error) {
			if fail.Load() {
				return "", time.Time{}, errors.New("private")
			}
			return "A", time.Time{}, nil
		})
		environment.trackCase(test, 101)
		fail.Store(true)
		environment.clock.Set(epoch.Add(time.Minute))

		// When
		_, firstError := environment.service.Scheduled(context.Background(), 101)
		environment.clock.Set(epoch.Add(119 * time.Second))
		before, secondError := environment.service.Status(context.Background(), 101)
		environment.clock.Set(epoch.Add(2 * time.Minute))
		_, thirdError := environment.service.Status(context.Background(), 101)

		// Then
		assert.ErrorIs(test, firstError, ErrCheck, "failed schedule guard")
		assert.NoError(test, secondError, "failed schedule guard")
		assert.ErrorIs(test, thirdError, ErrCheck, "failed schedule guard")
		assert.True(test, !before.Fresh, "failed schedule guard")
		assert.Equal(test, 3, environment.calls.Load(), "failed schedule guard")
	})

	test.Run("should deny work when owner is invalid", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		environment.trackCase(test, 101)

		// When
		_, err := environment.service.Scheduled(context.Background(), 0)

		// Then
		assert.ErrorIs(test, err, ErrDenied)
		assert.Equal(test, 1, environment.calls.Load(), "invalid owner should not trigger portal work")
	})

	test.Run("should reuse recent tracked case baseline without reservation when run after restart", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		id := environment.trackCase(test, 101)
		environment.reopen(test)
		var portalCalls atomic.Int32
		service := NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
			portalCalls.Add(1)
			return "", time.Time{}, nil
		}), Clock: environment.clock})
		environment.clock.Set(epoch.Add(59 * time.Second))

		// When
		status, statusErr := service.Scheduled(context.Background(), 101)
		next, guardErr := service.checkEligibility(context.Background(), 101, id, true)

		// Then
		assert.Equal(test, int32(0), portalCalls.Load(), "recent baseline should not trigger portal work")
		assert.NoError(test, statusErr, "recent tracked case baseline should be reused without reserving a check")
		assert.NoError(test, guardErr, "recent tracked case baseline should be reused without reserving a check")
		assert.True(test, !status.Fresh, "recent tracked case baseline should be reused without reserving a check")
		assert.True(test, next.IsZero(), "recent tracked case baseline should be reused without reserving a check")
		assert.True(test, status.NextEligibleAt.Equal(epoch.Add(time.Minute)), "recent tracked case baseline should be reused without reserving a check")
		verifyObservation(test, status, "A", epoch)
	})

	test.Run("should refresh any owner with a tracked case when no access policy is configured", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		environment.trackCase(test, 303)
		environment.clock.Set(epoch.Add(time.Minute))

		// When
		owners, inventoryErr := environment.service.ScheduledOwners(context.Background())
		status, err := environment.service.Scheduled(context.Background(), 303)
		_, otherErr := environment.service.Status(context.Background(), 404)

		// Then
		assert.NoError(test, inventoryErr)
		assert.Equal(test, 1, len(owners))
		assert.Equal(test, 303, owners[0])
		assert.NoError(test, err)
		assert.True(test, status.Fresh, "status.Fresh should hold")
		assert.Equal(test, 2, environment.calls.Load())
		assert.ErrorIs(test, otherErr, repositories.ErrNotFound, "another owner's case was accessible")
	})
}

func TestCaseServiceStoreCaseToTrack(test *testing.T) {
	test.Run("should deny work when owner is invalid", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		environment.trackCase(test, 101)

		// When
		_, _, err := environment.service.StoreCaseToTrack(context.Background(), 0, number, password)

		// Then
		assert.ErrorIs(test, err, ErrDenied)
		assert.Equal(test, 1, environment.calls.Load(), "invalid owner should not trigger portal work")
	})

	test.Run("should refuse replacement without portal check when owner already exists", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		id := environment.trackCase(test, 101)

		// When
		_, _, err := environment.service.StoreCaseToTrack(context.Background(), 101, number, password)
		trackedCase, read := environment.store.TrackedCases().FindByOwner(context.Background(), 101)

		// Then
		assert.ErrorIs(test, err, repositories.ErrExists, "duplicate tracked case should preserve the original case without portal work")
		assert.NoError(test, read, "duplicate tracked case should preserve the original case without portal work")
		assert.Equal(test, id, trackedCase.ID, "duplicate tracked case should preserve the original case without portal work")
		assert.Equal(test, 1, environment.calls.Load(), "duplicate tracked case should preserve the original case without portal work")
		assert.Equal(test, number, trackedCase.Number, "tracked case credentials missing")
		assert.Equal(test, password, trackedCase.Password, "tracked case credentials missing")
	})

	test.Run("should leave no committed case when portal check fails", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		ctx := context.Background()
		portal := portalFunc(func(context.Context, string, string) (string, time.Time, error) {
			return "", time.Time{}, errors.New("private remote detail")
		})
		service := NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portal, Clock: environment.clock})

		// When
		_, _, err := service.StoreCaseToTrack(ctx, 101, number, password)
		_, readError := environment.store.TrackedCases().FindByOwner(context.Background(), 101)

		// Then
		assert.True(test, err != nil, "failed case tracking setup should return an error")
		assert.True(test, !strings.Contains(err.Error(), "private"), "tracked case error should not expose remote details")
		assert.ErrorIs(test, readError, repositories.ErrNotFound, "failed case tracking setup should not persist data")
	})

	test.Run("should leave no committed case when store insert fails", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		ctx := context.Background()
		rejectInsert(test, environment.path, "tracked_cases")
		portal := portalFunc(func(context.Context, string, string) (string, time.Time, error) { return "A", time.Time{}, nil })
		service := NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portal, Clock: environment.clock})

		// When
		_, _, err := service.StoreCaseToTrack(ctx, 101, number, password)
		_, readError := environment.store.TrackedCases().FindByOwner(context.Background(), 101)

		// Then
		assert.True(test, err != nil, "failed case tracking setup should return an error")
		assert.True(test, !strings.Contains(err.Error(), "private"), "tracked case error should not expose remote details")
		assert.ErrorIs(test, readError, repositories.ErrNotFound, "failed case tracking setup should not persist data")
	})

	test.Run("should leave no committed case when caller is canceled", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		ctx := context.Background()
		canceledContext, cancel := context.WithCancel(ctx)
		cancel()
		ctx = canceledContext
		portal := portalFunc(func(context.Context, string, string) (string, time.Time, error) { return "A", time.Time{}, nil })
		service := NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portal, Clock: environment.clock})

		// When
		_, _, err := service.StoreCaseToTrack(ctx, 101, number, password)
		_, readError := environment.store.TrackedCases().FindByOwner(context.Background(), 101)

		// Then
		assert.True(test, err != nil, "failed case tracking setup should return an error")
		assert.True(test, !strings.Contains(err.Error(), "private"), "tracked case error should not expose remote details")
		assert.ErrorIs(test, readError, repositories.ErrNotFound, "failed case tracking setup should not persist data")
	})

	test.Run("should leave no committed case when password is empty", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		ctx := context.Background()
		portal := portalFunc(func(context.Context, string, string) (string, time.Time, error) { return "A", time.Time{}, nil })
		service := NewCaseService(CaseServiceConfig{Store: environment.store, Portal: portal, Clock: environment.clock})

		// When
		_, _, err := service.StoreCaseToTrack(ctx, 101, number, "")
		_, readError := environment.store.TrackedCases().FindByOwner(context.Background(), 101)

		// Then
		assert.True(test, err != nil, "failed case tracking setup should return an error")
		assert.True(test, !strings.Contains(err.Error(), "private"), "tracked case error should not expose remote details")
		assert.ErrorIs(test, readError, repositories.ErrNotFound, "failed case tracking setup should not persist data")
	})
}

func TestCaseServiceRemove(test *testing.T) {
	test.Run("should deny work when owner is invalid", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		environment.trackCase(test, 101)

		// When
		err := environment.service.Remove(context.Background(), 0)

		// Then
		assert.ErrorIs(test, err, ErrDenied)
		assert.Equal(test, 1, environment.calls.Load(), "invalid owner should not trigger portal work")
	})

	test.Run("should cancel and drain portal before deleting when late results arrive", func(test *testing.T) {
		// Given
		entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var block atomic.Bool
		environment := setup(test, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
			if block.Load() {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return "LATE", time.Time{}, nil
			}
			return "A", time.Time{}, nil
		})
		id := environment.trackCase(test, 101)
		block.Store(true)
		environment.clock.Set(epoch.Add(time.Minute))
		checked := make(chan answer, 1)
		go func() {
			status, err := environment.service.Status(context.Background(), 101)
			checked <- answer{status, err}
		}()
		<-entered
		removed := make(chan error, 1)

		// When
		go func() { removed <- environment.service.Remove(context.Background(), 101) }()
		<-canceled
		_, busyStatus := environment.service.Status(context.Background(), 101)
		_, _, busyStoreCaseToTrack := environment.service.StoreCaseToTrack(context.Background(), 101, number, password)
		before, beforeErr := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		select {
		case err := <-removed:
			assert.True(test, false, "removal should wait for in-flight work to drain, got %v", err)
		default:
		}
		close(release)
		late := <-checked
		removeErr := <-removed
		_, missing := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		block.Store(false)
		newID := environment.trackCase(test, 101)
		lateObservation := environment.service.saveObservation(context.Background(), 101, id, "LATE", environment.clock.Now())
		environment.clock.Set(epoch.Add(119 * time.Second))
		cached, statusErr := environment.service.Status(context.Background(), 101)

		// Then
		assert.ErrorIs(test, busyStatus, ErrRemoving, "removal should drain work, delete data, and retain the manual guard")
		assert.ErrorIs(test, busyStoreCaseToTrack, ErrRemoving, "removal should drain work, delete data, and retain the manual guard")
		assert.NoError(test, beforeErr, "removal should drain work, delete data, and retain the manual guard")
		assert.Equal(test, id, before.ID, "removal should drain work, delete data, and retain the manual guard")
		assert.ErrorIs(test, late.err, ErrRemoving, "removal should drain work, delete data, and retain the manual guard")
		assert.NoError(test, removeErr, "removal should drain work, delete data, and retain the manual guard")
		assert.ErrorIs(test, missing, repositories.ErrNotFound, "removal should drain work, delete data, and retain the manual guard")
		assert.True(test, newID != id, "removal should drain work, delete data, and retain the manual guard")
		assert.ErrorIs(test, lateObservation, repositories.ErrNotFound, "removal should drain work, delete data, and retain the manual guard")
		assert.NoError(test, statusErr, "removal should drain work, delete data, and retain the manual guard")
		assert.True(test, !cached.Fresh, "removal should drain work, delete data, and retain the manual guard")
		assert.True(test, cached.NextEligibleAt.Equal(epoch.Add(2*time.Minute)), "removal should drain work, delete data, and retain the manual guard")
		verifyObservation(test, cached, "A", epoch.Add(time.Minute))
	})

	test.Run("should prevent baseline commit when case tracking setup is canceled during check", func(test *testing.T) {
		// Given
		entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		environment := setup(test, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return "late", time.Time{}, nil
		})
		trackingStarted := make(chan error, 1)
		go func() {
			_, _, err := environment.service.StoreCaseToTrack(context.Background(), 101, number, password)
			trackingStarted <- err
		}()
		<-entered
		removed := make(chan error, 1)

		// When
		go func() { removed <- environment.service.Remove(context.Background(), 101) }()
		<-canceled
		_, _, busy := environment.service.StoreCaseToTrack(context.Background(), 101, number, password)
		close(release)
		trackCaseError, removeErr := <-trackingStarted, <-removed
		_, missing := environment.store.TrackedCases().FindByOwner(context.Background(), 101)

		// Then
		assert.ErrorIs(test, busy, ErrRemoving, "case tracking setup cancellation")
		assert.ErrorIs(test, trackCaseError, context.Canceled, "case tracking setup cancellation")
		assert.ErrorIs(test, removeErr, repositories.ErrNotFound, "case tracking setup cancellation")
		assert.ErrorIs(test, missing, repositories.ErrNotFound, "case tracking setup cancellation")
	})

	test.Run("should retain tracked case and reopen work when caller cancels during drain", func(test *testing.T) {
		// Given
		entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var block atomic.Bool
		environment := setup(test, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
			if block.Load() {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return "", time.Time{}, ctx.Err()
			}
			return "A", time.Time{}, nil
		})
		id := environment.trackCase(test, 101)
		environment.clock.Set(epoch.Add(time.Minute))
		block.Store(true)
		work := make(chan error, 1)
		go func() { _, err := environment.service.Status(context.Background(), 101); work <- err }()
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		removed := make(chan error, 1)

		// When
		go func() { removed <- environment.service.Remove(ctx, 101) }()
		<-canceled
		cancel()
		close(release)
		workErr, removeErr := <-work, <-removed
		trackedCase, readErr := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		status, statusErr := environment.service.Status(context.Background(), 101)

		// Then
		assert.ErrorIs(test, workErr, ErrRemoving, "canceled removal")
		assert.ErrorIs(test, removeErr, context.Canceled, "canceled removal")
		assert.NoError(test, readErr, "canceled removal")
		assert.Equal(test, id, trackedCase.ID, "canceled removal")
		assert.NoError(test, statusErr, "canceled removal")
		assert.True(test, !status.CheckedAt.IsZero(), "canceled removal")
	})
}

func TestCaseServiceShutdown(test *testing.T) {
	test.Run("should cancel and drain all owners when work is active", func(test *testing.T) {
		// Given
		started := make(chan struct{}, 2)
		cancelled := make(chan struct{}, 2)
		release := make(chan struct{})
		environment := setup(test, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
			started <- struct{}{}
			<-ctx.Done()
			cancelled <- struct{}{}
			<-release
			return "", time.Time{}, ctx.Err()
		})
		finished := make(chan error, 2)
		for _, owner := range []int64{101, 202} {
			go func(owner int64) {
				_, _, err := environment.service.StoreCaseToTrack(context.Background(), owner, number, password)
				finished <- err
			}(owner)
		}
		for index := 0; index < 2; index++ {
			<-started
		}
		done := make(chan struct{})

		// When
		go func() { environment.service.Shutdown(); close(done) }()
		for index := 0; index < 2; index++ {
			<-cancelled
		}

		// Then
		select {
		case <-done:
			assert.True(test, false, "shutdown should wait for all callbacks to drain")
		default:
		}
		close(release)
		<-done
		for index := 0; index < 2; index++ {
			{
				err := <-finished
				assert.ErrorIs(test, err, context.Canceled)
			}
		}
		environment.service.Shutdown()
		{
			_, _, err := environment.service.StoreCaseToTrack(context.Background(), 101, number, password)
			assert.ErrorIs(test, err, ErrClosed, "case tracking setup admitted after shutdown")
		}
		{
			err := environment.service.Remove(context.Background(), 101)
			assert.ErrorIs(test, err, ErrClosed, "removal admitted after shutdown")
		}
	})
}

func TestCaseServiceSaveObservation(test *testing.T) {
	test.Run("should roll back cached status when history insert fails", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		id := environment.trackCase(test, 101)
		db, err := sql.Open("sqlite", environment.path)
		assert.NoError(test, err)
		defer db.Close()
		{
			_, err := db.Exec(`CREATE TRIGGER reject_tracked_cases_history BEFORE INSERT ON tracked_cases_history
 BEGIN SELECT RAISE(ABORT, 'synthetic tracked_cases_history failure'); END;`)
			assert.NoError(test, err)
		}

		// When
		observeErr := environment.service.saveObservation(context.Background(), 101, id, "B", epoch.Add(time.Minute))
		trackedCase, readErr := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		history, historyErr := environment.store.TrackedCasesHistory().FindByTrackedCase(context.Background(), 101, id)

		// Then
		assert.ErrorIs(test, observeErr, repositories.ErrStorage, "observation rollback")
		assert.NoError(test, readErr, "observation rollback")
		assert.NoError(test, historyErr, "observation rollback")
		assert.Equal(test, "A", trackedCase.Stage, "observation rollback")
		assert.True(test, trackedCase.CheckedAt.Equal(epoch), "observation rollback")
		assert.Equal(test, 0, len(history), "observation rollback")
	})
}

func TestCaseServiceHasTrackedCase(test *testing.T) {
	test.Run("should signal fatal storage failure when database read fails for active caller", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		assert.NoError(test, environment.db.Close())
		reports := 0
		var fatalError error
		environment.service.onFatal = func(err error) {
			reports++
			fatalError = err
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// When
		_, err := environment.service.HasTrackedCase(ctx, 101)

		// Then
		assert.True(test, err != nil, "database read should return an error")
		assert.Equal(test, 1, reports, "only active storage failures should be reported")
		assert.ErrorIs(test, fatalError, ErrStorage)
	})

	test.Run("should not signal fatal storage failure when caller is canceled", func(test *testing.T) {
		// Given
		environment := setup(test, nil)
		assert.NoError(test, environment.db.Close())
		reports := 0
		environment.service.onFatal = func(error) { reports++ }
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancel()

		// When
		_, err := environment.service.HasTrackedCase(ctx, 101)

		// Then
		assert.True(test, err != nil, "database read should return an error")
		assert.Equal(test, 0, reports, "only active storage failures should be reported")
	})
}

// -- Internals ----------------------------------------------------------------

var epoch = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const number, password = "synthetic-number", "synthetic-password"

type portalFunc func(context.Context, string, string) (string, time.Time, error)

func (this portalFunc) Check(ctx context.Context, owner int64, number, password string) (string, time.Time, error) {
	return this(ctx, number, password)
}

type fixture struct {
	service *CaseService
	db      *sql.DB
	store   *repositories.Store
	clock   *clock.FixedClock
	path    string
	calls   atomic.Int32
}

func setup(test *testing.T, portal portalFunc) *fixture {
	test.Helper()
	environment := &fixture{clock: clock.NewFixedClock(epoch), path: filepath.Join(test.TempDir(), "state.db")}
	var err error
	environment.db, err = database.Open(context.Background(), environment.path)
	assert.NoError(test, err)
	environment.store = repositories.NewStore(environment.db)
	test.Cleanup(func() { environment.db.Close() })
	wrapped := portalFunc(func(ctx context.Context, number, password string) (string, time.Time, error) {
		environment.calls.Add(1)
		if portal != nil {
			return portal(ctx, number, password)
		}
		return "A", time.Time{}, nil
	})
	environment.service = NewCaseService(CaseServiceConfig{Store: environment.store, Portal: wrapped, Clock: environment.clock})
	return environment
}

func (this *fixture) reopen(test *testing.T) {
	test.Helper()
	{
		err := this.db.Close()
		assert.NoError(test, err)
	}
	db, err := database.Open(context.Background(), this.path)
	assert.NoError(test, err)
	this.db = db
	this.store = repositories.NewStore(db)
}

func (this *fixture) trackCase(test *testing.T, owner int64) string {
	test.Helper()
	{
		_, _, err := this.service.StoreCaseToTrack(context.Background(), owner, number, password)
		assert.NoError(test, err)
	}
	trackedCase, err := this.store.TrackedCases().FindByOwner(context.Background(), owner)
	assert.NoError(test, err)
	return trackedCase.ID
}

func verifyObservation(test *testing.T, status models.Status, stage string, checked time.Time) {
	test.Helper()
	assert.True(test, !status.CheckedAt.IsZero(), "observation should have the expected stage and completion timestamp")
	assert.Equal(test, stage, status.Stage, "observation should have the expected stage and completion timestamp")
	assert.True(test, status.CheckedAt.Equal(checked), "observation should have the expected stage and completion timestamp")
}

// Notifies when a caller reaches the cancellable shared-flight wait, avoiding
// sleeps or inspecting the coordinator's internal synchronization.
type waitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (this *waitContext) Done() <-chan struct{} {
	this.once.Do(func() { close(this.entered) })
	return this.Context.Done()
}

func waitingContext(ctx context.Context) *waitContext {
	return &waitContext{Context: ctx, entered: make(chan struct{})}
}

type answer struct {
	status models.Status
	err    error
}

func rejectInsert(test *testing.T, path, table string) {
	test.Helper()
	db, err := sql.Open("sqlite", path)
	assert.NoError(test, err)
	defer db.Close()
	assert.True(test, !(table != "tracked_cases_history" && table != "tracked_cases"), "invalid fixture table")
	{
		_, err := db.Exec("CREATE TRIGGER reject_insert BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'synthetic storage failure'); END;")
		assert.NoError(test, err)
	}
}

func (portalFunc) ForgetSession(int64) {}
