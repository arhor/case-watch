package repositories

import (
	"context"
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
