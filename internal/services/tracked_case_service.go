// Package services implements case tracking, status checks and removal.
// One TrackedCaseService is shared by the Telegram and scheduled-jobs controllers; transactions contain no network I/O.
package services

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/platform/guard"
)

var (
	ErrClosed   = errors.New("case service stopped")
	ErrDenied   = errors.New("access denied")
	ErrBusy     = errors.New("owner work in progress")
	ErrRemoving = errors.New("removal in progress")
	ErrCheck    = errors.New("portal check failed")
	ErrStorage  = errors.New("case storage operation failed")
	ErrInvalid  = errors.New("invalid tracked case input")
	ErrNotFound = domain.ErrNotFound
)

type TrackedCaseServiceConfig struct {
	Store   domain.Store
	Portal  domain.Portal
	Clock   domain.Clock
	OnFatal func(error)
}

var _ domain.TrackedCaseService = (*TrackedCaseService)(nil)

type TrackedCaseService struct {
	mu      sync.Mutex
	closed  bool
	owners  map[int64]*ownerState
	store   domain.Store
	onFatal func(error)
	portal  domain.Portal
	clock   domain.Clock
}

func NewTrackedCaseService(config TrackedCaseServiceConfig) *TrackedCaseService {
	return &TrackedCaseService{
		owners:  make(map[int64]*ownerState),
		store:   guard.Require("Store", config.Store),
		portal:  guard.Require("Portal", config.Portal),
		clock:   guard.Require("Clock", config.Clock),
		onFatal: config.OnFatal,
	}
}

// HasTrackedCase lets interactive entry points ask a business-level question
// without reading a tracked case repository directly.
func (this *TrackedCaseService) HasTrackedCase(ctx context.Context, owner int64) (bool, error) {
	if owner <= 0 {
		return false, ErrDenied
	}
	_, err := this.store.TrackedCases().FindByOwner(ctx, owner)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, this.storageError(ctx, err)
	}
	return true, nil
}

// ListTrackedCaseOwners returns the owners with tracked cases available for scheduler
// traversal. The scheduler entry point never reads repositories itself.
func (this *TrackedCaseService) ListTrackedCaseOwners(ctx context.Context) ([]int64, error) {
	items, err := this.store.TrackedCases().FindAll(ctx)
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

func (this *TrackedCaseService) StoreCaseToTrack(ctx context.Context, owner int64, number, password string) (string, time.Time, error) {
	if owner <= 0 {
		return "", time.Time{}, ErrDenied
	}
	if ctx.Err() != nil {
		return "", time.Time{}, ctx.Err()
	}
	if strings.TrimSpace(number) == "" || strings.TrimSpace(password) == "" {
		return "", time.Time{}, ErrInvalid
	}
	this.mu.Lock()
	if this.closed {
		this.mu.Unlock()
		return "", time.Time{}, ErrClosed
	}
	s := this.state(owner)
	if s.removing {
		this.mu.Unlock()
		return "", time.Time{}, ErrRemoving
	}
	if s.check != nil {
		this.mu.Unlock()
		return "", time.Time{}, ErrBusy
	}
	_, err := this.store.TrackedCases().FindByOwner(ctx, owner)
	if err == nil {
		this.mu.Unlock()
		return "", time.Time{}, domain.ErrExists
	}
	if !errors.Is(err, domain.ErrNotFound) {
		this.mu.Unlock()
		return "", time.Time{}, this.storageError(ctx, err)
	}
	work, cancel := context.WithCancel(ctx)
	f := &check{cancel: cancel, done: make(chan struct{}), creatingCase: true}
	s.check = f
	this.mu.Unlock()
	defer func() { cancel(); this.mu.Lock(); s.check = nil; close(f.done); this.mu.Unlock() }()
	committed := false
	defer func() {
		if !committed {
			this.portal.ForgetSession(owner)
		}
	}()
	id, err := domain.NewTrackedCaseID()
	if err != nil {
		return "", time.Time{}, ErrStorage
	}
	stage, checkedAt, err := this.baseline(work, owner, number, password)
	if err != nil {
		return "", time.Time{}, err
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if s.removing {
		return "", time.Time{}, ErrRemoving
	}
	if work.Err() != nil {
		return "", time.Time{}, work.Err()
	}
	if err = this.saveTrackedCase(work, owner, id, number, password, stage, checkedAt); err != nil {
		return "", time.Time{}, this.storageError(ctx, err)
	}
	committed = true
	return stage, checkedAt, nil
}

func (this *TrackedCaseService) CheckCaseStatus(ctx context.Context, owner int64) (domain.Status, error) {
	return this.check(ctx, owner, true)
}

// Scheduled and manual requests share active checks and the persisted case-start guard.
// A guarded case is skipped for this slot; no queued retry is created. It never
// consumes the user's manual guard. Recent successful cache is reused too.
func (this *TrackedCaseService) RunScheduledCaseCheck(ctx context.Context, owner int64) (domain.Status, error) {
	return this.check(ctx, owner, false)
}

// Remove requires confirmation at the conversation boundary. Once admitted it
// drains canceled work before returning, even when the caller cancels; cancellation
// then prevents deletion. Failed removal retains the tracked case and reopens work.
func (this *TrackedCaseService) RemoveTrackedCase(ctx context.Context, owner int64) error {
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
	trackedCase, err := this.store.TrackedCases().FindByOwner(ctx, owner)
	if err != nil {
		return this.storageError(ctx, err)
	}
	if trackedCase.OwnerID != owner {
		return ErrDenied
	}
	err = this.storageError(ctx, this.deleteTrackedCase(ctx, owner, trackedCase.ID))
	if err == nil {
		this.portal.ForgetSession(owner)
	}
	return err
}

// Shutdown permanently closes admission, cancels every owner check and drains
// work before store may close. Concurrent calls are safe and all wait for the
// same admitted work. Collaborators must honor cancellation.
func (this *TrackedCaseService) Shutdown() {
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

// -- Internals ----------------------------------------------------------------

const (
	caseCheckCooldown   = "case_check"
	manualCheckCooldown = "manual_check"
)

type check struct {
	cancel       context.CancelFunc
	done         chan struct{}
	status       domain.Status
	err          error
	waiters      int
	creatingCase bool
}

type ownerState struct {
	check       *check
	removing    bool
	removalDone chan struct{}
}

func (this *TrackedCaseService) state(owner int64) *ownerState {
	state := this.owners[owner]
	if state == nil {
		state = &ownerState{}
		this.owners[owner] = state
	}
	return state
}

func (this *TrackedCaseService) storageError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotFound
	}
	if errors.Is(err, domain.ErrExists) {
		return domain.ErrExists
	}
	if ctx.Err() == nil && this.onFatal != nil {
		this.onFatal(ErrStorage)
	}
	return ErrStorage
}

func canceled(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

func (this *TrackedCaseService) baseline(ctx context.Context, owner int64, number, password string) (string, time.Time, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	stage, _, err := this.portal.Check(bounded, owner, number, password)
	if err != nil {
		return "", time.Time{}, canceled(bounded, ErrCheck)
	}
	if err = bounded.Err(); err != nil {
		return "", time.Time{}, err
	}
	return stage, this.clock.Now(), nil
}

func cached(stage string, checkedAt time.Time) domain.Status {
	return domain.Status{Stage: stage, CheckedAt: checkedAt}
}

func (this *TrackedCaseService) check(ctx context.Context, owner int64, manual bool) (domain.Status, error) {
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
		if check.creatingCase {
			this.mu.Unlock()
			return domain.Status{}, ErrBusy
		}
		check.waiters++
		this.mu.Unlock()
		return this.wait(ctx, check, !manual)
	}
	trackedCase, err := this.store.TrackedCases().FindByOwner(ctx, owner)
	if err != nil {
		this.mu.Unlock()
		return domain.Status{}, this.storageError(ctx, err)
	}
	if trackedCase.OwnerID != owner {
		this.mu.Unlock()
		return domain.Status{}, ErrDenied
	}
	now := this.clock.Now()
	status := cached(trackedCase.Stage, trackedCase.CheckedAt)
	if now.Before(trackedCase.CheckedAt.Add(time.Minute)) {
		status.NextEligibleAt, err = this.checkEligibility(ctx, owner, trackedCase.ID, manual)
		status.NextEligibleAt = later(status.NextEligibleAt, trackedCase.CheckedAt.Add(time.Minute))
		this.mu.Unlock()
		return status, this.storageError(ctx, err)
	}
	reservation, err := this.reserveCheck(ctx, owner, trackedCase.ID, manual, now)
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
	go this.refresh(work, owner, state, check, trackedCase)
	return this.wait(ctx, check, !manual)
}

// Scheduled calls wait for their canceled check to finish before returning.
func (this *TrackedCaseService) wait(
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
			return check.status, check.err
		}

	case <-ctx.Done():
		{
			this.mu.Lock()
			check.waiters--
			if check.waiters == 0 {
				check.cancel()
			}
			status := check.status
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

func (this *TrackedCaseService) refresh(
	ctx context.Context,
	owner int64,
	state *ownerState,
	check *check,
	trackedCase domain.TrackedCase,
) {
	stage, checkedAt, err := this.baseline(ctx, owner, trackedCase.Number, trackedCase.Password)
	this.mu.Lock()

	defer this.mu.Unlock()
	defer check.cancel()

	if state.removing {
		err = ErrRemoving
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if err == nil {
		var change *domain.StageChange
		change, err = this.saveObservation(ctx, owner, trackedCase.ID, stage, checkedAt)
		err = this.storageError(ctx, err)

		if err == nil {
			next := check.status.NextEligibleAt
			check.status = cached(stage, checkedAt)
			check.status.Fresh = true
			check.status.Change = change
			check.status.NextEligibleAt = later(next, checkedAt.Add(time.Minute))
		}
	}

	check.err = err
	state.check = nil

	close(check.done)
}

func (this *TrackedCaseService) saveTrackedCase(
	ctx context.Context,
	owner int64,
	id string,
	number, password string,
	stage string,
	checkedAt time.Time,
) error {
	return this.store.RunInTx(ctx, func(store domain.Store) error {
		return store.TrackedCases().Create(ctx, owner, id, number, password, stage, checkedAt)
	})
}

func (this *TrackedCaseService) saveObservation(
	ctx context.Context,
	owner int64,
	id string,
	stage string,
	checkedAt time.Time,
) (*domain.StageChange, error) {
	var change *domain.StageChange
	err := this.store.RunInTx(ctx, func(store domain.Store) error {
		trackedCase, err := store.TrackedCases().FindByIdentity(ctx, owner, id)
		if err != nil {
			return err
		}
		if err = store.TrackedCases().UpdateObservation(ctx, owner, id, stage, checkedAt); err != nil {
			return err
		}
		if trackedCase.Stage == stage {
			return nil
		}
		if err := store.TrackedCasesHistory().Append(ctx, id, trackedCase.Stage, stage, checkedAt); err != nil {
			return err
		}
		change = &domain.StageChange{PreviousStage: trackedCase.Stage, Stage: stage, CheckedAt: checkedAt}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return change, nil
}

func (this *TrackedCaseService) deleteTrackedCase(
	ctx context.Context,
	owner int64,
	id string,
) error {
	return this.store.RunInTx(ctx, func(store domain.Store) error {
		if err := store.TrackedCases().SoftDelete(ctx, owner, id, time.Now().UTC()); err != nil {
			return err
		}
		if err := store.TrackedCasesHistory().DeleteByTrackedCaseID(ctx, id); err != nil {
			return err
		}
		return store.Cooldowns().DeleteBySubjectTypeAndSubjectID(ctx, caseCheckCooldown, id)
	})
}

func (this *TrackedCaseService) checkEligibility(
	ctx context.Context,
	owner int64,
	id string,
	manual bool,
) (time.Time, error) {
	var next time.Time
	err := this.store.RunInTx(ctx, func(store domain.Store) error {
		var err error
		next, err = eligibility(ctx, store, owner, id, manual)
		return err
	})
	return next, err
}

func (this *TrackedCaseService) reserveCheck(
	ctx context.Context,
	owner int64,
	id string,
	manual bool,
	now time.Time,
) (domain.CheckReservation, error) {
	if now.IsZero() {
		return domain.CheckReservation{}, domain.ErrInvalid
	}
	var result domain.CheckReservation
	err := this.store.RunInTx(ctx, func(store domain.Store) error {
		next, err := eligibility(ctx, store, owner, id, manual)
		if err != nil {
			return err
		}
		result.NextEligibleAt = next
		if now.Before(next) {
			return nil
		}
		if err = store.Cooldowns().Upsert(ctx, domain.Cooldown{
			SubjectType: caseCheckCooldown,
			SubjectID:   id,
			StartedAt:   now,
		}); err != nil {
			return err
		}
		if manual {
			if err = store.Cooldowns().Upsert(ctx, domain.Cooldown{
				SubjectType: manualCheckCooldown,
				SubjectID:   strconv.FormatInt(owner, 10),
				StartedAt:   now,
			}); err != nil {
				return err
			}
		}
		result = domain.CheckReservation{
			Reserved:       true,
			NextEligibleAt: now.Add(time.Minute),
		}
		return nil
	})
	if err != nil {
		return domain.CheckReservation{}, err
	}
	return result, nil
}

func eligibility(
	ctx context.Context,
	store domain.Store,
	owner int64,
	id string,
	manual bool,
) (time.Time, error) {
	if _, err := store.TrackedCases().FindByIdentity(ctx, owner, id); err != nil {
		return time.Time{}, err
	}
	c, err := store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, caseCheckCooldown, id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return time.Time{}, err
	}
	caseStart := c.StartedAt
	if manual {
		c, err = store.Cooldowns().FindBySubjectTypeAndSubjectID(ctx, manualCheckCooldown, strconv.FormatInt(owner, 10))
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return time.Time{}, err
		}
		if c.StartedAt.After(caseStart) {
			caseStart = c.StartedAt
		}
	}
	if caseStart.IsZero() {
		return time.Time{}, nil
	}
	return caseStart.Add(time.Minute), nil
}
