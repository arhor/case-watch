package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/tracking"
)

var ctx = context.Background()
var epoch = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func observation(stage string, n int) application.Observation {
	return application.Observation{Stage: stage, CheckedAt: epoch.Add(time.Duration(n) * time.Minute)}
}
func persistenceFixture(t *testing.T) (*persistenceStore, string, *credentials.Keyring, application.SealedCredentials) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := openPersistenceDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	k, err := credentials.Parse([]byte(`{"version":1,"active_key_id":"synthetic","keys":{"synthetic":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := data.NewEnrollmentID()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := k.Seal(id, application.Credentials{Number: "SYNTHETIC-NUMBER-PRIVATE", Password: "SYNTHETIC-PASSWORD-PRIVATE"})
	if err != nil {
		t.Fatal(err)
	}
	return s, path, k, sealed
}
func enroll(t *testing.T, s *persistenceStore, c application.SealedCredentials) {
	t.Helper()
	if err := (tracking.Persistence{DB: s.Store}).Enroll(ctx, 101, c, observation("A", 0)); err != nil {
		t.Fatal(err)
	}
}
func count(t *testing.T, s *persistenceStore, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestObserveShouldPreserveOccurrencesWhenStagesReturnToBaseline(t *testing.T) {
	// Given
	s, path, k, c := persistenceFixture(t)
	enroll(t, s, c)
	// When
	for i, stage := range []string{"A", "B", "B", "A"} {
		if err := (tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation(stage, i+1)); err != nil {
			t.Fatal(err)
		}
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
	history, err := reopened.History.FindByEnrollment(ctx, 101, c.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("history count %d error %v", len(history), err)
	}
	if history[0].PreviousStage != "A" || history[0].Stage != "B" || history[1].Stage != "A" || history[0].Sequence >= history[1].Sequence {
		t.Fatal("lost transition occurrences")
	}
	if count(t, reopened, "pending") != 2 {
		t.Fatal("lost pending work")
	}
	e, err := reopened.Enrollments.FindByOwner(ctx, 101)
	if err != nil || e.Observation.Stage != "A" || !e.Observation.CheckedAt.Equal(observation("A", 4).CheckedAt) {
		t.Fatal("cache not persisted")
	}
	if _, err = k.Open(e.Credentials); err != nil {
		t.Fatal(err)
	}
}
func TestObserveShouldRefreshTimeOnlyWhenStageIsUnchanged(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	// When
	err := (tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("A", 2))
	// Then
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Enrollments.FindByOwner(ctx, 101)
	if err != nil || !e.Observation.CheckedAt.Equal(observation("A", 2).CheckedAt) || count(t, s, "history") != 0 || count(t, s, "pending") != 0 {
		t.Fatal("unchanged observation changed history or lost time")
	}
}
func TestObserveShouldCommitOnceWhenConcurrentObservationsMatch(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	// When
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- (tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
		}()
	}
	wg.Wait()
	close(errs)
	// Then
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count(t, s, "history") != 1 || count(t, s, "pending") != 1 {
		t.Fatal("duplicated observation")
	}
}
func TestObserveShouldRollbackEveryEffectWhenPersistenceFails(t *testing.T) {
	// Given
	s, path, _, c := persistenceFixture(t)
	enroll(t, s, c)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_pending BEFORE INSERT ON pending BEGIN SELECT RAISE(ABORT,'synthetic sensitive driver detail'); END;`); err != nil {
		t.Fatal(err)
	}
	// When
	err := (tracking.Persistence{DB: s.Store}).Observe(ctx, 101, c.ID, observation("B", 1))
	s.Close()
	reopened, openErr := openPersistenceDatabase(ctx, path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer reopened.Close()
	// Then
	if !errors.Is(err, data.ErrStorage) || err.Error() != data.ErrStorage.Error() {
		t.Fatal("error was not sanitized")
	}
	e, err := reopened.Enrollments.FindByOwner(ctx, 101)
	if err != nil || e.Observation.Stage != "A" || !e.Observation.CheckedAt.Equal(epoch) || count(t, reopened, "history") != 0 || count(t, reopened, "pending") != 0 {
		t.Fatal("partial observation survived rollback")
	}
}
func TestEnrollShouldRollbackCredentialsAndIdentityWhenBaselineFails(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_enrollment BEFORE INSERT ON enrollments BEGIN SELECT RAISE(ABORT,'failure'); END;`); err != nil {
		t.Fatal(err)
	}
	// When
	err := (tracking.Persistence{DB: s.Store}).Enroll(ctx, 101, c, observation("A", 0))
	// Then
	if !errors.Is(err, data.ErrStorage) || count(t, s, "enrollments") != 0 {
		t.Fatal("partial enrollment committed")
	}
}
func TestEnrollShouldRefuseReplacementWhenOwnerAlreadyExists(t *testing.T) {
	// Given
	s, _, k, c := persistenceFixture(t)
	enroll(t, s, c)
	id, err := data.NewEnrollmentID()
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := k.Seal(id, application.Credentials{Number: "other", Password: "other"})
	if err != nil {
		t.Fatal(err)
	}
	// When
	err = (tracking.Persistence{DB: s.Store}).Enroll(ctx, 101, replacement, observation("B", 1))
	// Then
	if !errors.Is(err, data.ErrExists) || count(t, s, "enrollments") != 1 {
		t.Fatal("owner replacement accepted")
	}
	e, err := s.Enrollments.FindByOwner(ctx, 101)
	if err != nil || e.Credentials.ID != c.ID || e.Observation.Stage != "A" {
		t.Fatal("original changed")
	}
}
func TestStorageShouldDenyOtherOwnerWhenIdentityIsKnown(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	// When
	observeErr := (tracking.Persistence{DB: s.Store}).Observe(ctx, 202, c.ID, observation("B", 1))
	_, readErr := s.Enrollments.FindByOwner(ctx, 202)
	history, historyErr := s.History.FindByEnrollment(ctx, 202, c.ID)
	// Then
	if !errors.Is(observeErr, data.ErrNotFound) || !errors.Is(readErr, data.ErrNotFound) || historyErr != nil || len(history) != 0 || count(t, s, "history") != 0 {
		t.Fatal("ownership bypass")
	}
}
func TestOpenShouldRejectFutureSchemaWhenDatabaseAlreadyExists(t *testing.T) {
	// Given
	s, path, _, c := persistenceFixture(t)
	enroll(t, s, c)
	if _, err := s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// When
	opened, openErr := openPersistenceDatabase(ctx, path)
	// Then
	if opened != nil {
		opened.Close()
		t.Fatal("future schema opened")
	}
	if !errors.Is(openErr, data.ErrSchema) {
		t.Fatal(openErr)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("future database modified")
	}
}
func TestStorageShouldKeepCredentialsSealedWhenWrittenAndReopened(t *testing.T) {
	// Given
	s, path, k, c := persistenceFixture(t)
	enroll(t, s, c)
	id, err := data.NewEnrollmentID()
	if err != nil {
		t.Fatal(err)
	}
	other, err := k.Seal(id, application.Credentials{Number: "other", Password: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if err = (tracking.Persistence{DB: s.Store}).Enroll(ctx, 202, other, observation("A", 0)); err != nil {
		t.Fatal(err)
	}
	// When
	_, err = s.db.Exec("UPDATE enrollments SET payload=? WHERE owner_id=?", c.Payload, 202)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Enrollments.FindByOwner(ctx, 202)
	if err != nil {
		t.Fatal(err)
	}
	_, bindingErr := k.Open(e.Credentials)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE enrollments SET stage=? WHERE owner_id=?", "B", 101); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatal(err)
	}
	// Then
	if bindingErr == nil {
		t.Fatal("copied ciphertext authenticated")
	}
	if len(files) < 2 {
		t.Fatal("rollback journal was not inspected")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, plaintext := range []string{"SYNTHETIC-NUMBER-PRIVATE", "SYNTHETIC-PASSWORD-PRIVATE"} {
			if bytes.Contains(data, []byte(plaintext)) {
				t.Fatal("plaintext reached database or journal")
			}
		}
	}
}

// persistenceStore keeps raw SQL fault injection local to integration tests.
type persistenceStore struct {
	*data.Store
	db *sql.DB
}

func openPersistenceDatabase(ctx context.Context, path string) (*persistenceStore, error) {
	s, err := data.NewStore(ctx, path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		s.Close()
		return nil, err
	}
	return &persistenceStore{Store: s, db: db}, nil
}
func (s *persistenceStore) Close() error { s.db.Close(); return s.Store.Close() }
