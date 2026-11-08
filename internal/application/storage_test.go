package application

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseFileShouldRestrictPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	for _, existing := range []bool{false, true} {
		if existing {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := databaseFile(path); err != nil {
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

func TestAcquireInstanceLockShouldDistinguishStartupFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.db")
	lock, err := acquireInstanceLock(path)
	if lock != nil || !errors.Is(err, errStartup) || errors.Is(err, errLocked) {
		t.Fatalf("unexpected lock result: %v %v", lock, err)
	}
}
