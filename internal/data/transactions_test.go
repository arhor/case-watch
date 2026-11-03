package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

func txEnrollment() (application.SealedCredentials, application.Observation) {
	return application.SealedCredentials{ID: "00000000000000000000000000000001", FormatVersion: 1, KeyID: "synthetic", Payload: make([]byte, 28)}, application.Observation{Stage: "A", CheckedAt: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
}
func writeAcrossRepositories(ctx context.Context, tx *Store) error {
	c, o := txEnrollment()
	if err := tx.Enrollments.Create(ctx, 101, c, o); err != nil {
		return err
	}
	if err := tx.Guards.RecordCaseStart(ctx, c.ID, o.CheckedAt); err != nil {
		return err
	}
	sequence, err := tx.History.Append(ctx, c.ID, "A", application.Observation{Stage: "B", CheckedAt: o.CheckedAt})
	if err != nil {
		return err
	}
	if err = tx.Pending.Create(ctx, sequence, 101); err != nil {
		return err
	}
	if err = tx.Recipients.SetState(ctx, 101, RecipientState{Blocked: true, Pause: "blocked"}); err != nil {
		return err
	}
	return nil
}
func assertTransactionRows(t *testing.T, s *Store, want int) {
	t.Helper()
	for _, table := range []string{"enrollments", "case_guards", "history", "pending", "recipients"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s rows=%d, want %d", table, n, want)
		}
	}

}
func TestRunInTxShouldCommitEveryRepositoryWhenCallbackSucceeds(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	// When
	err := s.RunInTx(context.Background(), func(tx *Store) error { return writeAcrossRepositories(context.Background(), tx) })
	// Then
	if err != nil {
		t.Fatal(err)
	}
	assertTransactionRows(t, s, 1)
}
func TestRunInTxShouldRollbackEveryRepositoryWhenCallbackFails(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	failure := errors.New("callback failure")
	// When
	err := s.RunInTx(context.Background(), func(tx *Store) error {
		if err := writeAcrossRepositories(context.Background(), tx); err != nil {
			return err
		}
		return failure
	})
	// Then
	if err != failure {
		t.Fatalf("callback error identity lost: %v", err)
	}
	assertTransactionRows(t, s, 0)
}
func TestRunInTxShouldRollbackAndPropagatePanicWhenCallbackPanics(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	failure := errors.New("panic identity")
	var caught any
	// When
	func() {
		defer func() { caught = recover() }()
		_ = s.RunInTx(context.Background(), func(tx *Store) error {
			if err := writeAcrossRepositories(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
			panic(failure)
		})
	}()
	// Then
	if caught != failure {
		t.Fatalf("panic identity lost: %v", caught)
	}
	assertTransactionRows(t, s, 0)
}
func TestRunInTxShouldRollbackWhenContextIsCanceledBeforeCommit(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When
	err := s.RunInTx(ctx, func(tx *Store) error {
		if err := writeAcrossRepositories(ctx, tx); err != nil {
			return err
		}
		cancel()
		return nil
	})
	// Then
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("unsanitized commit error: %v", err)
	}
	assertTransactionRows(t, s, 0)
}
func TestRunInTxShouldRejectNestedLifecycleCallsWhenStoreIsTransactionBound(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	called := false
	var nested, closeErr error
	// When
	err := s.RunInTx(context.Background(), func(tx *Store) error {
		nested = tx.RunInTx(context.Background(), func(*Store) error { called = true; return nil })
		closeErr = tx.Close()
		return writeAcrossRepositories(context.Background(), tx)
	})
	// Then
	if called || !errors.Is(nested, ErrTransaction) || !errors.Is(closeErr, ErrTransaction) || err != nil {
		t.Fatalf("nested=%v close=%v callback=%v invoked=%v", nested, closeErr, err, called)
	}
	assertTransactionRows(t, s, 1)
}
func TestRunInTxShouldSanitizeBeginFailureWhenDatabaseIsClosed(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	// When
	err := s.RunInTx(context.Background(), func(*Store) error { called = true; return nil })
	// Then
	if called || err != ErrStorage {
		t.Fatalf("error=%v callback invoked=%v", err, called)
	}
}
func TestRunInTxShouldRollbackWhenCommitRejectsDeferredConstraint(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	// When
	err := s.RunInTx(context.Background(), func(tx *Store) error {
		if _, err := tx.Pending.db.ExecContext(context.Background(), "PRAGMA defer_foreign_keys=ON"); err != nil {
			return ErrStorage
		}
		return tx.Pending.Create(context.Background(), 999, 101)
	})
	// Then
	if err != ErrStorage {
		t.Fatalf("unsanitized commit failure: %v", err)
	}
	assertTransactionRows(t, s, 0)
}
