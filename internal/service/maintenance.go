package service

import (
	"context"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
)

// Maintain opens an existing database only while the service is stopped. Every
// operation, including inspection, authenticates all credentials without reset.
func Maintain(ctx context.Context, db, key string, fn func(*data.Store, *credentials.Keyring, string) error) (result error) {
	db, key, err := Paths(db, key)
	if err != nil {
		return err
	}

	keyring, err := credentials.Load(key)
	if err != nil {
		return ErrStartup
	}

	lock, err := Acquire(db)
	if err != nil {
		return err
	}
	defer lock.Close()

	if err = databaseFile(db, false); err != nil {
		return err
	}

	store, err := data.Open(ctx, db)
	if err != nil {
		return ErrStartup
	}

	defer func() {
		if err := store.Close(); err != nil {
			result = ErrService
		}
	}()

	if err = Verify(ctx, store, keyring); err != nil {
		return err
	}
	if err = fn(store, keyring, key); err != nil {
		return ErrService
	}
	return nil
}

// Rotate persists old+new keys with the old active ID, commits all ciphertext,
// verifies committed rows, then activates the new ID durably. Retained keys make
// every interruption recoverable. Hook is an offline fault-injection boundary.
func Rotate(ctx context.Context, s *data.Store, k *credentials.Keyring, path, id string, hook func(string) error) error {
	stage := func(s string) error {
		if hook != nil {
			return hook(s)
		}
		return nil
	}
	prepared, e := k.Prepare(id)
	if e != nil {
		return ErrService
	}
	if e = prepared.Replace(path); e != nil {
		return ErrService
	}
	if e = stage("keys_persisted"); e != nil {
		return ErrService
	}
	active, e := prepared.Activate(id)
	if e != nil {
		return ErrService
	}
	e = RewriteCredentials(ctx, s, func(r application.SealedCredentials) (application.SealedCredentials, error) {
		plain, e := prepared.Open(r)
		if e != nil {
			return application.SealedCredentials{}, e
		}
		defer func() { plain = application.Credentials{} }()
		if e = stage("record"); e != nil {
			return application.SealedCredentials{}, e
		}
		return active.Seal(r.ID, plain)
	})
	if e != nil {
		return ErrService
	}
	if e = stage("ciphertext_committed"); e != nil {
		return ErrService
	}
	if e = Verify(ctx, s, prepared); e != nil {
		return ErrService
	}
	if e = stage("verified"); e != nil {
		return ErrService
	}
	if e = active.Replace(path); e != nil {
		return ErrService
	}
	return nil
}

// RewriteCredentials commits every active credential rewrite together. The callback
// performs local cryptography only; keyring file writes belong outside RunInTx.
func RewriteCredentials(ctx context.Context, s *data.Store, rewrite func(application.SealedCredentials) (application.SealedCredentials, error)) error {
	return s.RunInTx(ctx, func(tx *data.Store) error {
		rows, err := tx.Enrollments.FindAll(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			original := row.Credentials
			replacement, err := rewrite(original)
			if err != nil {
				return data.ErrStorage
			}
			if replacement.ID != original.ID {
				return data.ErrInvalid
			}
			if err = tx.Enrollments.UpdateCredentials(ctx, replacement); err != nil {
				return err
			}
		}
		return nil
	})
}
