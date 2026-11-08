package repositories

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/arhor/case-watch/internal/repositories/migrations"
)

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Portal credentials are stored here, so the directory and database file
	// must not be readable by other users.
	dir, err := os.Stat(filepath.Dir(absPath))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("database directory must be private")
	}

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
