package data

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"net/url"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.up.sql
var migrations embed.FS

func openDB(ctx context.Context, path string) (*sql.DB, error) {
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
	if err = migrateDB(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrateDB(ctx context.Context, db *sql.DB) error {
	if ctx.Err() != nil {
		return ErrStorage
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return ErrStorage
	}
	defer source.Close()
	var legacy, tables int
	if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&legacy) != nil {
		return ErrStorage
	}
	if legacy != 0 {
		return ErrSchema
	}
	if db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&tables) != nil {
		return ErrStorage
	}
	if tables == 0 {
		var objects int
		if db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&objects) != nil {
			return ErrStorage
		}
		if objects != 0 {
			return ErrSchema
		}
	} else {
		rows, err := db.QueryContext(ctx, "SELECT version,dirty FROM schema_migrations")
		if err != nil {
			return ErrSchema
		}
		count := 0
		for rows.Next() {
			var version int
			var dirty bool
			if rows.Scan(&version, &dirty) != nil || dirty || version < 0 {
				rows.Close()
				return ErrSchema
			}
			body, _, readErr := source.ReadUp(uint(version))
			if readErr != nil {
				rows.Close()
				return ErrSchema
			}
			body.Close()
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || count > 1 {
			return ErrSchema
		}
		if count == 0 {
			var objects int
			if db.QueryRowContext(ctx, `
				SELECT count(*)
				  FROM sqlite_master
				 WHERE name NOT LIKE 'sqlite_%'
				   AND name NOT IN ('schema_migrations','version_unique')
			`).Scan(&objects) != nil {
				return ErrStorage
			}
			if objects != 0 {
				return ErrSchema
			}
		}
	}
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return ErrStorage
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return ErrStorage
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			select {
			case runner.GracefulStop <- true:
			case <-done:
			}
		case <-done:
		}
	}()
	err = runner.Up()
	close(done)
	<-stopped

	if ctx.Err() != nil {
		return ErrStorage
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return ErrStorage
	}
	return nil
}
