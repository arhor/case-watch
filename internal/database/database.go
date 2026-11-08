package database

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/arhor/case-watch/internal/database/migrations"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
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
