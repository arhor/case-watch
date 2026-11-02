package data

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestOpenShouldRejectSchemaWhenUnversionedDatabaseContainsTables(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "existing.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE enrollments(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// When
	opened, openErr := Open(ctx, path)
	// Then
	if opened != nil {
		opened.Close()
		t.Fatal("unversioned populated database opened")
	}
	if !errors.Is(openErr, ErrSchema) {
		t.Fatal(openErr)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("database modified")
	}
}

func TestOpenShouldRejectSchemaWhenMetadataIsInvalid(t *testing.T) {
	for _, query := range []string{
		"PRAGMA user_version=1",
		"PRAGMA user_version=2",
		"UPDATE schema_migrations SET version=99",
		"UPDATE schema_migrations SET dirty=1",
		"UPDATE schema_migrations SET version=-1",
		"ALTER TABLE schema_migrations RENAME COLUMN dirty TO invalid",
		"DROP TABLE schema_migrations",
		"DELETE FROM schema_migrations",
	} {
		t.Run(query, func(t *testing.T) {
			// Given
			s, path, _, _ := fixture(t)
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			s.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// When
			opened, err := Open(ctx, path)
			// Then
			if opened != nil {
				opened.Close()
				t.Fatal("unsupported schema opened")
			}
			if !errors.Is(err, ErrSchema) {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("database modified")
			}
		})
	}
}

func TestMigrationShouldRollbackSQLWhenStatementFails(t *testing.T) {
	// Given
	s, path, _, _ := fixture(t)
	files := fstest.MapFS{
		"migrations/000001_initial.up.sql": &fstest.MapFile{Data: []byte("-- existing")},
		"migrations/000002_failure.up.sql": &fstest.MapFile{Data: []byte("CREATE TABLE synthetic(id INTEGER); INSERT INTO nonexistent VALUES(1);")},
	}
	// When
	err := s.migrateFrom(ctx, files)
	// Then
	if !errors.Is(err, ErrStorage) {
		t.Fatal(err)
	}
	var objects int
	if e := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='synthetic'").Scan(&objects); e != nil || objects != 0 {
		t.Fatal("partial SQL committed", e)
	}
	var version int
	var dirty bool
	if e := s.db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); e != nil || version != 2 || !dirty {
		t.Fatal("missing dirty state", e)
	}
	s.Close()
	opened, e := Open(ctx, path)
	if opened != nil {
		opened.Close()
		t.Fatal("dirty database opened")
	}
	if !errors.Is(e, ErrSchema) {
		t.Fatal(e)
	}
}

func TestMigrationShouldApplyOrderedChangesWhenNewVersionsExist(t *testing.T) {
	// Given
	s, _, _, _ := fixture(t)
	defer s.Close()
	files := fstest.MapFS{
		"migrations/000001_initial.up.sql": &fstest.MapFile{Data: []byte("-- existing")},
		"migrations/000002_create.up.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE synthetic(id INTEGER);")},
		"migrations/000003_insert.up.sql":  &fstest.MapFile{Data: []byte("INSERT INTO synthetic VALUES(42);")},
	}
	// When
	err := s.migrateFrom(ctx, files)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	var value int
	if e := s.db.QueryRow("SELECT id FROM synthetic").Scan(&value); e != nil || value != 42 {
		t.Fatal("upgrade failed", e)
	}
}
