package data

import (
	"context"
	"embed"
	"errors"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// ADR: docs/adr/0010_adopt-golang-migrate.md.
//
//go:embed migrations/*.up.sql
var migrations embed.FS

func (s *Store) migrate(ctx context.Context) error { return s.migrateFrom(ctx, migrations) }

func (s *Store) migrateFrom(ctx context.Context, files fs.FS) error {
	if ctx.Err() != nil {
		return ErrStorage
	}
	source, err := iofs.New(files, "migrations")
	if err != nil {
		return ErrStorage
	}
	defer source.Close()
	var legacy, tables int
	if s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&legacy) != nil {
		return ErrStorage
	}
	if legacy != 0 {
		return ErrSchema
	}
	if s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&tables) != nil {
		return ErrStorage
	}
	if tables == 0 {
		var objects int
		if s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&objects) != nil {
			return ErrStorage
		}
		if objects != 0 {
			return ErrSchema
		}
	} else {
		rows, err := s.db.QueryContext(ctx, "SELECT version,dirty FROM schema_migrations")
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
			if s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND name NOT IN ('schema_migrations','version_unique')").Scan(&objects) != nil {
				return ErrStorage
			}
			if objects != 0 {
				return ErrSchema
			}
		}
	}
	driver, err := sqlite.WithInstance(s.db, &sqlite.Config{})
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
