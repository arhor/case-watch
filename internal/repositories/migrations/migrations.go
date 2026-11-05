package migrations

import (
	"database/sql"
	"embed"
	"errors"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed *.sql
var migrations embed.FS

func Run(db *sql.DB) error {
	source, err := iofs.New(migrations, ".")
	if err != nil {
		return err
	}
	defer source.Close()

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}
