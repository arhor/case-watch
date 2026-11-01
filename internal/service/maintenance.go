package service

import (
	"context"
	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/storage"
	"time"
)

// Maintain opens an existing database only while the service is stopped. Every
// operation, including inspection, authenticates all credentials without reset.
func Maintain(ctx context.Context, db, key string, fn func(*storage.Store, *credentials.Keyring, string) error) (result error) {
	db, key, e := Paths(db, key)
	if e != nil {
		return e
	}
	k, e := credentials.Load(key)
	if e != nil {
		return ErrStartup
	}
	lock, e := Acquire(db)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = databaseFile(db, false); e != nil {
		return e
	}
	s, e := storage.Open(ctx, db)
	if e != nil {
		return ErrStartup
	}
	defer func() {
		if s.Close() != nil {
			result = ErrService
		}
	}()
	if e = Verify(ctx, s, k); e != nil {
		return e
	}
	if e = fn(s, k, key); e != nil {
		return ErrService
	}
	return nil
}

// Rotate persists old+new keys with the old active ID, commits all ciphertext,
// verifies committed rows, then activates the new ID durably. Retained keys make
// every interruption recoverable. Hook is an offline fault-injection boundary.
func Rotate(ctx context.Context, s *storage.Store, k *credentials.Keyring, path, id string, hook func(string) error) error {
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
	e = s.RewriteCredentials(ctx, func(r application.SealedCredentials) (application.SealedCredentials, error) {
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
func Resume(ctx context.Context, s *storage.Store, recipient int64, token bool) error {
	if e := s.RecoverDeliveries(ctx, time.Now()); e != nil {
		return ErrService
	}
	if token {
		return s.ResumeToken(ctx)
	}
	return s.ResumeRecipient(ctx, recipient)
}
