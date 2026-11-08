package repositories

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDBShouldProtectDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	for _, existing := range []bool{false, true} {
		if existing {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		db, err := openDB(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("existing=%t: got permissions %04o, want 0600", existing, got)
		}
	}
}

func TestNewExclusiveStoreShouldReleaseInstanceLockOnClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")

	first, err := NewExclusiveStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewExclusiveStore(context.Background(), path)
	if second != nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("expected another instance to be rejected, got %v %v", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewExclusiveStore(context.Background(), path)
	if err != nil {
		t.Fatalf("instance lock not released on close: %v", err)
	}
	defer reopened.Close()
}

func TestNewExclusiveStoreShouldRejectSharedDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewExclusiveStore(context.Background(), filepath.Join(dir, "state.db"))
	if store != nil {
		store.Close()
	}
	if err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("unexpected result for shared directory: %v", err)
	}
}
