// Package cases implements enrollment, status checks and removal.
// One Service is shared by the bot and scheduler; transactions contain no network I/O.
package cases

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
)

var (
	ErrClosed   = errors.New("case service stopped")
	ErrDenied   = errors.New("access denied")
	ErrBusy     = errors.New("owner work in progress")
	ErrRemoving = errors.New("removal in progress")
	ErrCheck    = errors.New("portal check failed")
	ErrStorage  = errors.New("case storage operation failed")
	ErrInvalid  = errors.New("invalid enrollment input")
	ErrNotFound = repositories.ErrNotFound
)

type check struct {
	cancel     context.CancelFunc
	done       chan struct{}
	status     domain.Status
	err        error
	waiters    int
	enrollment bool
}

type ownerState struct {
	check       *check
	removing    bool
	removalDone chan struct{}
}

type Service struct {
	mu             sync.Mutex
	closed         bool
	owners         map[int64]*ownerState
	db             *repositories.Store
	onStorageError func()
	portal         domain.Portal
	clock          domain.Clock
}

type Config struct {
	DB             *repositories.Store
	Portal         domain.Portal
	Clock          domain.Clock
	OnStorageError func()
}

func New(config Config) *Service {
	return &Service{
		owners:         make(map[int64]*ownerState),
		db:             config.DB,
		portal:         config.Portal,
		clock:          config.Clock,
		onStorageError: config.OnStorageError,
	}
}

// HasEnrollment lets interactive entry points ask a business-level question
// without reading an enrollment repository directly.
func (c *Service) HasEnrollment(ctx context.Context, owner int64) (bool, error) {
	if owner <= 0 {
		return false, ErrDenied
	}
	_, err := c.db.Enrollments.FindByOwner(ctx, owner)
	if errors.Is(err, repositories.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, c.storageError(ctx, err)
	}
	return true, nil
}

// ScheduledOwners returns the enrolled owners available for scheduler
// traversal. The scheduler entry point never reads repositories itself.
func (c *Service) ScheduledOwners(ctx context.Context) ([]int64, error) {
	items, err := c.db.Enrollments.FindAll(ctx)
	if err != nil {
		return nil, c.storageError(ctx, err)
	}
	owners := make([]int64, 0, len(items))
	for _, item := range items {
		if item.OwnerID > 0 {
			owners = append(owners, item.OwnerID)
		}
	}
	return owners, nil
}

func (c *Service) Enroll(ctx context.Context, owner int64, credentials domain.Credentials) (domain.Observation, error) {
	defer func() { credentials = domain.Credentials{} }()
	if owner <= 0 {
		return domain.Observation{}, ErrDenied
	}
	if ctx.Err() != nil {
		return domain.Observation{}, ctx.Err()
	}
	if strings.TrimSpace(credentials.Number) == "" || strings.TrimSpace(credentials.Password) == "" {
		return domain.Observation{}, ErrInvalid
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return domain.Observation{}, ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return domain.Observation{}, ErrRemoving
	}
	if s.check != nil {
		c.mu.Unlock()
		return domain.Observation{}, ErrBusy
	}
	_, err := c.db.Enrollments.FindByOwner(ctx, owner)
	if err == nil {
		c.mu.Unlock()
		return domain.Observation{}, repositories.ErrExists
	}
	if !errors.Is(err, repositories.ErrNotFound) {
		c.mu.Unlock()
		return domain.Observation{}, c.storageError(ctx, err)
	}
	work, cancel := context.WithCancel(ctx)
	f := &check{cancel: cancel, done: make(chan struct{}), enrollment: true}
	s.check = f
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); s.check = nil; close(f.done); c.mu.Unlock() }()
	id, err := repositories.NewEnrollmentID()
	if err != nil {
		return domain.Observation{}, ErrStorage
	}
	o, err := c.baseline(work, credentials)
	if err != nil {
		return domain.Observation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.removing {
		return domain.Observation{}, ErrRemoving
	}
	if work.Err() != nil {
		return domain.Observation{}, work.Err()
	}
	if err = c.saveEnrollment(work, owner, id, credentials, o); err != nil {
		return domain.Observation{}, c.storageError(ctx, err)
	}
	return o, nil
}

func (c *Service) Status(ctx context.Context, owner int64) (domain.Status, error) {
	return c.check(ctx, owner, true)
}

// Scheduled and manual requests share active checks and the persisted case-start guard.
// A guarded case is skipped for this slot; no queued retry is created. It never
// consumes the user's manual guard. Recent successful cache is reused too.
func (c *Service) Scheduled(ctx context.Context, owner int64) (domain.Status, error) {
	return c.check(ctx, owner, false)
}

// Remove requires confirmation at the conversation boundary. Once admitted it
// drains canceled work before returning, even when the caller cancels; cancellation
// then prevents deletion. Failed removal retains the enrollment and reopens work.
func (c *Service) Remove(ctx context.Context, owner int64) error {
	if owner <= 0 {
		return ErrDenied
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return ErrRemoving
	}
	s.removing = true
	s.removalDone = make(chan struct{})
	defer func() { c.mu.Lock(); s.removing = false; close(s.removalDone); s.removalDone = nil; c.mu.Unlock() }()
	f := s.check
	if f != nil {
		f.cancel()
	}
	c.mu.Unlock()
	if f != nil {
		<-f.done
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e, err := c.db.Enrollments.FindByOwner(ctx, owner)
	if err != nil {
		return c.storageError(ctx, err)
	}
	if e.OwnerID != owner {
		return ErrDenied
	}
	return c.storageError(ctx, c.deleteEnrollment(ctx, owner, e.ID))
}

// Shutdown permanently closes admission, cancels every owner check and drains
// work before storage may close. Concurrent calls are safe and all wait for the
// same admitted work. Collaborators must honor cancellation.
func (c *Service) Shutdown() {
	c.mu.Lock()
	c.closed = true
	var done []<-chan struct{}
	for _, s := range c.owners {
		if s.check != nil {
			s.check.cancel()
			done = append(done, s.check.done)
		}
		if s.removalDone != nil {
			done = append(done, s.removalDone)
		}
	}
	c.mu.Unlock()
	for _, ch := range done {
		<-ch
	}
}

// -- Internal implementation ---------------------------------------------------------------------

func (c *Service) state(owner int64) *ownerState {
	s := c.owners[owner]
	if s == nil {
		s = &ownerState{}
		c.owners[owner] = s
	}
	return s
}

func (c *Service) storageError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, repositories.ErrNotFound) {
		return repositories.ErrNotFound
	}
	if errors.Is(err, repositories.ErrExists) {
		return repositories.ErrExists
	}
	if ctx.Err() == nil && c.onStorageError != nil {
		c.onStorageError()
	}
	return ErrStorage
}

func canceled(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

func (c *Service) baseline(ctx context.Context, credentials domain.Credentials) (domain.Observation, error) {
	defer func() { credentials = domain.Credentials{} }()
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	o, err := c.portal.Check(bounded, credentials)
	if err != nil {
		return domain.Observation{}, canceled(bounded, ErrCheck)
	}
	if err = bounded.Err(); err != nil {
		return domain.Observation{}, err
	}
	// Completion time belongs to this case service, not a supplied portal timestamp.
	o.CheckedAt = c.clock.Now()
	return o, nil
}

func cached(o domain.Observation) domain.Status {
	return domain.Status{Observation: &o}
}

func (c *Service) check(ctx context.Context, owner int64, manual bool) (domain.Status, error) {
	if owner <= 0 {
		return domain.Status{}, ErrDenied
	}
	if ctx.Err() != nil {
		return domain.Status{}, ctx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return domain.Status{}, ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return domain.Status{}, ErrRemoving
	}
	if f := s.check; f != nil {
		if f.enrollment {
			c.mu.Unlock()
			return domain.Status{}, ErrBusy
		}
		f.waiters++
		c.mu.Unlock()
		return c.wait(ctx, f, !manual)
	}
	e, err := c.db.Enrollments.FindByOwner(ctx, owner)
	if err != nil {
		c.mu.Unlock()
		return domain.Status{}, c.storageError(ctx, err)
	}
	if e.OwnerID != owner {
		c.mu.Unlock()
		return domain.Status{}, ErrDenied
	}
	now := c.clock.Now()
	status := cached(e.Observation)
	if now.Before(e.Observation.CheckedAt.Add(time.Minute)) {
		status.NextEligibleAt, err = c.checkEligibility(ctx, owner, e.ID, manual)
		status.NextEligibleAt = later(status.NextEligibleAt, e.Observation.CheckedAt.Add(time.Minute))
		c.mu.Unlock()
		return status, c.storageError(ctx, err)
	}
	reservation, err := c.reserveCheck(ctx, owner, e.ID, manual, now)
	if err != nil {
		c.mu.Unlock()
		return status, c.storageError(ctx, err)
	}
	status.NextEligibleAt = reservation.NextEligibleAt
	if !reservation.Reserved {
		c.mu.Unlock()
		return status, nil
	}
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f := &check{cancel: cancel, done: make(chan struct{}), waiters: 1, status: status}
	s.check = f
	c.mu.Unlock()
	go c.refresh(work, owner, s, f, e)
	return c.wait(ctx, f, !manual)
}

// Scheduled calls wait for their canceled check to finish before returning.
func (c *Service) wait(ctx context.Context, f *check, drain bool) (domain.Status, error) {
	defer func() {
		if drain && ctx.Err() != nil {
			<-f.done
		}
	}()
	select {
	case <-f.done:
		return copyStatus(f.status), f.err
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
		}
		status := copyStatus(f.status)
		status.InProgress = true
		c.mu.Unlock()
		return status, ctx.Err()
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func copyStatus(s domain.Status) domain.Status {
	if s.Observation != nil {
		o := *s.Observation
		s.Observation = &o
	}
	return s
}

func (c *Service) refresh(ctx context.Context, owner int64, s *ownerState, f *check, e repositories.Enrollment) {
	credentials := e.Credentials
	o, err := c.baseline(ctx, credentials)
	credentials = domain.Credentials{}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer f.cancel()
	if s.removing {
		err = ErrRemoving
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if err == nil {
		err = c.storageError(ctx, c.saveObservation(ctx, owner, e.ID, o))
		if err == nil {
			next := f.status.NextEligibleAt
			f.status = cached(o)
			f.status.Fresh = true
			f.status.NextEligibleAt = later(next, o.CheckedAt.Add(time.Minute))
		}
	}
	f.err = err
	s.check = nil
	close(f.done)
}
