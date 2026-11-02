package data

import (
	"context"
	"path/filepath"
	"testing"
)

var ctx = context.Background()

func databaseFixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func TestOpenShouldUseDurableSettingsWhenConnectionIsInitialized(t *testing.T) {
	// Given
	s, _ := databaseFixture(t)
	// When
	var journal string
	var syncMode, foreign, maxConnections int
	err1 := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal)
	err2 := s.db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	err3 := s.db.QueryRow("PRAGMA foreign_keys").Scan(&foreign)
	maxConnections = s.db.Stats().MaxOpenConnections
	// Then
	if err1 != nil || err2 != nil || err3 != nil || journal != "delete" || syncMode != 2 || foreign != 1 || maxConnections != 1 {
		t.Fatal("unsafe connection settings")
	}
}
