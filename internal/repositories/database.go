package repositories

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite"

	"github.com/arhor/case-watch/internal/repositories/migrations"
)

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// SQLite stores portal credentials; protect the file on creation and reopen.
	file, err := os.OpenFile(absPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}

	uriPath := filepath.ToSlash(absPath)
	if vol := filepath.VolumeName(absPath); len(vol) == 2 && vol[1] == ':' {
		uriPath = "/" + uriPath
	}

	uri := url.URL{Scheme: "file", Path: uriPath}
	params := url.Values{}
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(FULL)")
	params.Add("_pragma", "busy_timeout(5000)")
	uri.RawQuery = params.Encode()

	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrations.Run(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

var ErrLocked = errors.New("another case-watch instance is already running")

// openExclusiveDB keeps the bot's single-instance lock for as long as the
// returned database is owned by its Store. SQLite manages its own transactions.
func openExclusiveDB(ctx context.Context, path string) (*sql.DB, *os.File, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}

	// The deployed bot stores portal credentials in a private data directory.
	dir, err := os.Stat(filepath.Dir(absPath))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o077 != 0 {
		return nil, nil, errors.New("database directory must be private")
	}

	lock, err := os.OpenFile(absPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	// Do not unlink the lock file: another process may be waiting on its inode.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil, ErrLocked
		}
		return nil, nil, err
	}

	db, err := openDB(ctx, absPath)
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	return db, lock, nil
}
