// Package cases implements enrollment, status checks and removal.
// One Service is shared by the bot and scheduler; transactions contain no network I/O.
package cases

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/database/repositories"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/utils"
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
	db             *store.Store
	onStorageError func()
	portal         domain.Portal
	clock          utils.Clock
}

type Config struct {
	DB             *store.Store
	Portal         domain.Portal
	Clock          utils.Clock
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
func (this *Service) HasEnrollment(ctx context.Context, owner int64) (bool, error) {
	if owner <= 0 {
		return false, ErrDenied
	}
	_, err := this.db.Enrollments.FindByOwner(ctx, owner)
	if errors.Is(err, repositories.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, this.storageError(ctx, err)
	}
	return true, nil
}

// ScheduledOwners returns the enrolled owners available for scheduler
// traversal. The scheduler entry point never reads repositories itself.
func (this *Service) ScheduledOwners(ctx context.Context) ([]int64, error) {
	items, err := this.db.Enrollments.FindAll(ctx)
	if err != nil {
		return nil, this.storageError(ctx, err)
	}
	owners := make([]int64, 0, len(items))
	for _, item := range items {
		if item.OwnerID > 0 {
			owners = append(owners, item.OwnerID)
		}
	}
	return owners, nil
}

func (this *Service) Enroll(ctx context.Context, owner int64, Number, Password string) (domain.Observation, error) {
	if owner <= 0 {
		return domain.Observation{}, ErrDenied
	}
	if ctx.Err() != nil {
		return domain.Observation{}, ctx.Err()
	}
	if strings.TrimSpace(Number) == "" || strings.TrimSpace(Password) == "" {
		return domain.Observation{}, ErrInvalid
	}
	this.mu.Lock()
	if this.closed {
		this.mu.Unlock()
		return domain.Observation{}, ErrClosed
	}
	s := this.state(owner)
	if s.removing {
		this.mu.Unlock()
		return domain.Observation{}, ErrRemoving
	}
	if s.check != nil {
		this.mu.Unlock()
		return domain.Observation{}, ErrBusy
	}
	_, err := this.db.Enrollments.FindByOwner(ctx, owner)
	if err == nil {
		this.mu.Unlock()
		return domain.Observation{}, repositories.ErrExists
	}
	if !errors.Is(err, repositories.ErrNotFound) {
		this.mu.Unlock()
		return domain.Observation{}, this.storageError(ctx, err)
	}
	work, cancel := context.WithCancel(ctx)
	f := &check{cancel: cancel, done: make(chan struct{}), enrollment: true}
	s.check = f
	this.mu.Unlock()
	defer func() { cancel(); this.mu.Lock(); s.check = nil; close(f.done); this.mu.Unlock() }()
	id, err := repositories.NewEnrollmentID()
	if err != nil {
		return domain.Observation{}, ErrStorage
	}
	o, err := this.baseline(work, Number, Password)
	if err != nil {
		return domain.Observation{}, err
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if s.removing {
		return domain.Observation{}, ErrRemoving
	}
	if work.Err() != nil {
		return domain.Observation{}, work.Err()
	}
	if err = this.saveEnrollment(work, owner, id, Number, Password, o); err != nil {
		return domain.Observation{}, this.storageError(ctx, err)
	}
	return o, nil
}

func (this *Service) Status(ctx context.Context, owner int64) (domain.Status, error) {
	return this.check(ctx, owner, true)
}

// Scheduled and manual requests share active checks and the persisted case-start guard.
// A guarded case is skipped for this slot; no queued retry is created. It never
// consumes the user's manual guard. Recent successful cache is reused too.
func (this *Service) Scheduled(ctx context.Context, owner int64) (domain.Status, error) {
	return this.check(ctx, owner, false)
}

// Remove requires confirmation at the conversation boundary. Once admitted it
// drains canceled work before returning, even when the caller cancels; cancellation
// then prevents deletion. Failed removal retains the enrollment and reopens work.
func (this *Service) Remove(ctx context.Context, owner int64) error {
	if owner <= 0 {
		return ErrDenied
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	this.mu.Lock()
	if this.closed {
		this.mu.Unlock()
		return ErrClosed
	}
	s := this.state(owner)
	if s.removing {
		this.mu.Unlock()
		return ErrRemoving
	}
	s.removing = true
	s.removalDone = make(chan struct{})
	defer func() {
		this.mu.Lock()
		s.removing = false
		close(s.removalDone)
		s.removalDone = nil
		this.mu.Unlock()
	}()
	f := s.check
	if f != nil {
		f.cancel()
	}
	this.mu.Unlock()
	if f != nil {
		<-f.done
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e, err := this.db.Enrollments.FindByOwner(ctx, owner)
	if err != nil {
		return this.storageError(ctx, err)
	}
	if e.OwnerID != owner {
		return ErrDenied
	}
	return this.storageError(ctx, this.deleteEnrollment(ctx, owner, e.ID))
}

// Shutdown permanently closes admission, cancels every owner check and drains
// work before storage may close. Concurrent calls are safe and all wait for the
// same admitted work. Collaborators must honor cancellation.
func (this *Service) Shutdown() {
	this.mu.Lock()
	this.closed = true
	var done []<-chan struct{}
	for _, s := range this.owners {
		if s.check != nil {
			s.check.cancel()
			done = append(done, s.check.done)
		}
		if s.removalDone != nil {
			done = append(done, s.removalDone)
		}
	}
	this.mu.Unlock()
	for _, ch := range done {
		<-ch
	}
}

// -- Internal implementation ---------------------------------------------------------------------

func (this *Service) state(owner int64) *ownerState {
	state := this.owners[owner]
	if state == nil {
		state = &ownerState{}
		this.owners[owner] = state
	}
	return state
}

func (this *Service) storageError(ctx context.Context, err error) error {
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
	if ctx.Err() == nil && this.onStorageError != nil {
		this.onStorageError()
	}
	return ErrStorage
}

func canceled(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

func (this *Service) baseline(ctx context.Context, Number, Password string) (domain.Observation, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	observation, err := this.portal.Check(bounded, Number, Password)
	if err != nil {
		return domain.Observation{}, canceled(bounded, ErrCheck)
	}
	if err = bounded.Err(); err != nil {
		return domain.Observation{}, err
	}
	observation.CheckedAt = this.clock.Now()
	return observation, nil
}

func cached(observation domain.Observation) domain.Status {
	return domain.Status{Observation: &observation}
}

func (this *Service) check(ctx context.Context, owner int64, manual bool) (domain.Status, error) {
	if owner <= 0 {
		return domain.Status{}, ErrDenied
	}
	if ctx.Err() != nil {
		return domain.Status{}, ctx.Err()
	}
	this.mu.Lock()
	if this.closed {
		this.mu.Unlock()
		return domain.Status{}, ErrClosed
	}
	state := this.state(owner)
	if state.removing {
		this.mu.Unlock()
		return domain.Status{}, ErrRemoving
	}
	if check := state.check; check != nil {
		if check.enrollment {
			this.mu.Unlock()
			return domain.Status{}, ErrBusy
		}
		check.waiters++
		this.mu.Unlock()
		return this.wait(ctx, check, !manual)
	}
	enrollment, err := this.db.Enrollments.FindByOwner(ctx, owner)
	if err != nil {
		this.mu.Unlock()
		return domain.Status{}, this.storageError(ctx, err)
	}
	if enrollment.OwnerID != owner {
		this.mu.Unlock()
		return domain.Status{}, ErrDenied
	}
	now := this.clock.Now()
	status := cached(enrollment.Observation)
	if now.Before(enrollment.Observation.CheckedAt.Add(time.Minute)) {
		status.NextEligibleAt, err = this.checkEligibility(ctx, owner, enrollment.ID, manual)
		status.NextEligibleAt = later(status.NextEligibleAt, enrollment.Observation.CheckedAt.Add(time.Minute))
		this.mu.Unlock()
		return status, this.storageError(ctx, err)
	}
	reservation, err := this.reserveCheck(ctx, owner, enrollment.ID, manual, now)
	if err != nil {
		this.mu.Unlock()
		return status, this.storageError(ctx, err)
	}
	status.NextEligibleAt = reservation.NextEligibleAt
	if !reservation.Reserved {
		this.mu.Unlock()
		return status, nil
	}
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	check := &check{cancel: cancel, done: make(chan struct{}), waiters: 1, status: status}
	state.check = check
	this.mu.Unlock()
	go this.refresh(work, owner, state, check, enrollment)
	return this.wait(ctx, check, !manual)
}

// Scheduled calls wait for their canceled check to finish before returning.
func (this *Service) wait(
	ctx context.Context,
	check *check,
	drain bool,
) (domain.Status, error) {
	defer func() {
		if drain && ctx.Err() != nil {
			<-check.done
		}
	}()

	select {
	case <-check.done:
		{
			return copyStatus(check.status), check.err
		}

	case <-ctx.Done():
		{
			this.mu.Lock()
			check.waiters--
			if check.waiters == 0 {
				check.cancel()
			}
			status := copyStatus(check.status)
			status.InProgress = true
			this.mu.Unlock()
			return status, ctx.Err()
		}
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// WTF?!
func copyStatus(status domain.Status) domain.Status {
	if status.Observation != nil {
		observation := *status.Observation
		status.Observation = &observation
	}
	return status
}

func (this *Service) refresh(
	ctx context.Context,
	owner int64,
	state *ownerState,
	check *check,
	enrollment repositories.Enrollment,
) {
	observation, err := this.baseline(ctx, enrollment.Number, enrollment.Password)
	this.mu.Lock()

	defer this.mu.Unlock()
	defer check.cancel()

	if state.removing {
		err = ErrRemoving
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if err == nil {
		err = this.storageError(ctx, this.saveObservation(ctx, owner, enrollment.ID, observation))

		if err == nil {
			next := check.status.NextEligibleAt
			check.status = cached(observation)
			check.status.Fresh = true
			check.status.NextEligibleAt = later(next, observation.CheckedAt.Add(time.Minute))
		}
	}

	check.err = err
	state.check = nil

	close(check.done)
}
