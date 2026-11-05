package experimental

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// ----------------------------------------------------------------------------
// EXPERIMENTAL CODE, DON'T USE THIS IN PRODUCTION
// Goal: try to implement a persistent job scheduler using SQLite.
// ----------------------------------------------------------------------------

var (
	ErrAlreadyRunning = errors.New("scheduler: already running")
	ErrNoSuchJob      = errors.New("scheduler: job not found")
	ErrNotFailed      = errors.New("scheduler: job is not failed")
	ErrClaimLost      = errors.New("scheduler: job claim is no longer valid")
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusFailed  Status = "failed"
)

type int12 int

type Job struct {
	ID          int64
	Kind        string
	Payload     json.RawMessage
	RunAt       time.Time
	Status      Status
	Attempts    int
	MaxAttempts int
	RepeatEvery time.Duration
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Handler runs outside a database transaction. It MUST be idempotent if its
// side effects are external to SQLite, because it may be re-run after a crash.
type Handler func(context.Context, Job) error

// TxHandler runs in a transaction that also removes the successfully completed
// job. Use tx (not db) for every database mutation to get atomic effects.
// Keep this handler short: SQLite serializes write transactions.
type TxHandler func(context.Context, *sql.Tx, Job) error

type jobHandler struct {
	normal Handler
	tx     TxHandler
}

// Scheduler owns no database connection; the caller closes *sql.DB after Run exits.
type Scheduler struct {
	db           *sql.DB
	workers      int
	pollInterval time.Duration
	handlersMu   sync.RWMutex
	handlers     map[string]jobHandler
	wake         chan struct{}
	running      atomic.Bool
}

// New requires a SQLite *sql.DB. Configure it with a single open connection
// for simple and predictable write serialization (see OpenSQLite).
func New(db *sql.DB, workers int) *Scheduler {
	if db == nil {
		panic("scheduler: nil database connection")
	}
	if workers < 1 {
		panic("scheduler: workers must be at least 1")
	}
	return &Scheduler{
		db:           db,
		workers:      workers,
		pollInterval: time.Second,
		handlers:     make(map[string]jobHandler),
		wake:         make(chan struct{}, 1),
	}
}

// Register adds an ordinary (at-least-once) handler.
// Register all handlers before Run to avoid surprising missing-handler jobs.
func (s *Scheduler) Register(kind string, handler Handler) error {
	if handler == nil {
		return errors.New("scheduler: nil handler")
	}
	return s.register(kind, jobHandler{normal: handler})
}

// RegisterTx adds a handler whose SQLite effects and acknowledgement commit atomically.
func (s *Scheduler) RegisterTx(kind string, handler TxHandler) error {
	if handler == nil {
		return errors.New("scheduler: nil transactional handler")
	}
	return s.register(kind, jobHandler{tx: handler})
}

func (s *Scheduler) register(kind string, h jobHandler) error {
	if kind == "" {
		return errors.New("scheduler: empty job kind")
	}
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()
	if s.running.Load() {
		return errors.New("scheduler: cannot register while running")
	}
	if _, exists := s.handlers[kind]; exists {
		return fmt.Errorf("scheduler: duplicate handler for %q", kind)
	}
	s.handlers[kind] = h
	return nil
}

// Enqueue adds a durable one-off job. maxAttempts includes the first attempt.
// runAt may be in the past to make the job eligible immediately.
func (s *Scheduler) Enqueue(ctx context.Context, kind string, payload any, runAt time.Time, maxAttempts int) (int64, error) {
	return s.enqueue(ctx, kind, payload, runAt, maxAttempts, 0)
}

// EnqueueEvery creates a recurring job. After each successful occurrence,
// the next one is scheduled atomically with completion of the current one.
// The interval starts at the time of successful completion, so missed ticks
// are not backfilled. A permanently failed occurrence stops the recurrence.
func (s *Scheduler) EnqueueEvery(ctx context.Context, kind string, payload any, firstRun time.Time, every time.Duration, maxAttempts int) (int64, error) {
	if every < time.Millisecond {
		return 0, errors.New("scheduler: recurring interval must be at least 1ms")
	}
	return s.enqueue(ctx, kind, payload, firstRun, maxAttempts, every)
}

func (s *Scheduler) enqueue(ctx context.Context, kind string, payload any, runAt time.Time, maxAttempts int, every time.Duration) (int64, error) {
	if kind == "" || runAt.IsZero() || maxAttempts < 1 {
		return 0, errors.New("scheduler: kind, runAt, and positive maxAttempts are required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("scheduler: marshal payload: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
        INSERT INTO scheduled_jobs (kind, payload, run_at_ms, max_attempts, repeat_ms, created_ms)
        VALUES (?, ?, ?, ?, ?, ?)
    `, kind, data, runAt.UnixMilli(), maxAttempts, every.Milliseconds(), time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("scheduler: enqueue: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("scheduler: inserted job id: %w", err)
	}
	s.notify()
	return id, nil
}

// Run blocks until ctx is canceled or a database error occurs. A single
// Scheduler must not have two concurrent Run calls. Only one application
// process may manage this queue; no cross-process leader election is included.
func (s *Scheduler) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer s.running.Store(false)

	// On a fresh process start, no old handler can still be executing.
	// A crashed attempt has no durable outcome; give it another chance.
	// Only errors explicitly recorded by fail consume the retry budget.
	if _, err := s.db.ExecContext(ctx, `
        UPDATE scheduled_jobs
           SET status = 'pending',
               attempts = MAX(0, attempts - 1)
         WHERE status = 'running'
    `); err != nil {
		return fmt.Errorf("scheduler: recover interrupted jobs: %w", err)
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range s.workers {
		wg.Go(func() {
			if err := s.worker(workCtx); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case errorsCh <- err:
				default:
				}
				cancel()
			}
		})
	}
	wg.Wait()
	select {
	case err := <-errorsCh:
		return err
	default:
		return nil // ctx cancellation is a normal shutdown
	}
}

func (s *Scheduler) worker(ctx context.Context) error {
	for ctx.Err() == nil {
		job, err := s.claim(ctx)
		if err != nil {
			return err
		}
		if job == nil {
			if err := s.wait(ctx); err != nil {
				return err
			}
			continue
		}

		s.handlersMu.RLock()
		handler, found := s.handlers[job.Kind]
		s.handlersMu.RUnlock()
		if !found {
			// Unknown job types are retained as failed, not silently discarded.
			if err := s.fail(ctx, *job, fmt.Errorf("unregistered job kind %q", job.Kind), true); err != nil {
				return err
			}
			continue
		}

		if handler.tx != nil {
			// Transactional handlers finish the job in their own transaction.
			// Transaction commit errors are fatal, because outcome is ambiguous.
			execErr, fatalErr := s.executeTx(ctx, *job, handler.tx)
			if fatalErr != nil {
				return fatalErr
			}
			if ctx.Err() != nil {
				return ctx.Err() // Leave claimed; next process startup recovers it.
			}
			if execErr != nil {
				if err := s.fail(ctx, *job, execErr, false); err != nil {
					return err
				}
			}
			continue
		}

		execErr := handler.normal(ctx, *job)
		if execErr != nil {
			if ctx.Err() != nil {
				return ctx.Err() // Do not retry during shutdown.
			}
			if err := s.fail(ctx, *job, execErr, false); err != nil {
				return err
			}
		} else {
			// Handler succeeded; try to persist the acknowledgement even if
			// the run context was canceled while it finished.
			ackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := s.complete(ackCtx, *job)
			cancel()
			if err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (s *Scheduler) claim(ctx context.Context) (*Job, error) {
	const query = `
        UPDATE scheduled_jobs
           SET status = 'running', attempts = attempts + 1
         WHERE id = (
               SELECT id
                 FROM scheduled_jobs
                WHERE status = 'pending' AND run_at_ms <= ?
                ORDER BY run_at_ms, id
                LIMIT 1
         )
           AND status = 'pending'
        RETURNING id, kind, payload, run_at_ms, status, attempts, max_attempts,
                  repeat_ms, COALESCE(last_error, '')
    `
	var job Job
	var runAtMS, repeatMS int64
	err := s.db.QueryRowContext(ctx, query, time.Now().UnixMilli()).Scan(
		&job.ID, &job.Kind, &job.Payload, &runAtMS, &job.Status,
		&job.Attempts, &job.MaxAttempts, &repeatMS, &job.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scheduler: claim: %w", err)
	}
	job.RunAt = time.UnixMilli(runAtMS).UTC()
	job.RepeatEvery = time.Duration(repeatMS) * time.Millisecond
	return &job, nil
}

func (s *Scheduler) executeTx(ctx context.Context, job Job, handler TxHandler) (executionError, fatalError error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: begin transaction for job %d: %w", job.ID, err)
	}
	defer tx.Rollback()

	if err := handler(ctx, tx, job); err != nil {
		return err, nil
	}
	if err := finishTx(ctx, tx, job); err != nil {
		return nil, fmt.Errorf("scheduler: finish job %d in transaction: %w", job.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("scheduler: commit job %d: %w", job.ID, err)
	}
	return nil, nil
}

func (s *Scheduler) complete(ctx context.Context, job Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("scheduler: complete job %d: %w", job.ID, err)
	}
	defer tx.Rollback()
	if err := finishTx(ctx, tx, job); err != nil {
		return fmt.Errorf("scheduler: complete job %d: %w", job.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scheduler: commit completion of job %d: %w", job.ID, err)
	}
	return nil
}

// finishTx atomically removes this occurrence and creates the next one (if
// recurring). When using TxHandler it shares the transaction with job effects.
func finishTx(ctx context.Context, tx *sql.Tx, job Job) error {
	if job.RepeatEvery > 0 {
		now := time.Now()
		result, err := tx.ExecContext(ctx, `
            INSERT INTO scheduled_jobs
                (kind, payload, run_at_ms, max_attempts, repeat_ms, created_ms)
            VALUES (?, ?, ?, ?, ?, ?)
        `, job.Kind, []byte(job.Payload), now.Add(job.RepeatEvery).UnixMilli(),
			job.MaxAttempts, job.RepeatEvery.Milliseconds(), now.UnixMilli())
		if err != nil {
			return fmt.Errorf("schedule next occurrence: %w", err)
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `
        DELETE FROM scheduled_jobs
         WHERE id = ? AND status = 'running' AND attempts = ?
    `, job.ID, job.Attempts)
	if err != nil {
		return err
	}
	return requireOne(result)
}

func (s *Scheduler) fail(ctx context.Context, job Job, executionError error, permanent bool) error {
	status := "pending"
	if permanent || job.Attempts >= job.MaxAttempts {
		status = "failed"
	}
	nextRun := time.Now().Add(backoff(job.Attempts)).UnixMilli()
	message := executionError.Error()
	if len(message) > 4096 {
		message = message[:4096]
	}
	result, err := s.db.ExecContext(ctx, `
        UPDATE scheduled_jobs
           SET status = ?, run_at_ms = ?, last_error = ?
         WHERE id = ? AND status = 'running' AND attempts = ?
    `, status, nextRun, message, job.ID, job.Attempts)
	if err != nil {
		return fmt.Errorf("scheduler: mark job %d as %s: %w", job.ID, status, err)
	}
	if err := requireOne(result); err != nil {
		return fmt.Errorf("scheduler: mark job %d as %s: %w", job.ID, status, err)
	}
	if status == "pending" {
		s.notify()
	}
	return nil
}

func backoff(attempt int) time.Duration {
	// 1s, 2s, 4s, ..., up to 1 minute. Avoid arithmetic overflow.
	exponent := max(0, min(attempt-1, 6))
	delay := time.Second << exponent
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func requireOne(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrClaimLost
	}
	return nil
}

func (s *Scheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) wait(ctx context.Context) error {
	var next sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
        SELECT MIN(run_at_ms) FROM scheduled_jobs WHERE status = 'pending'
    `).Scan(&next); err != nil {
		return fmt.Errorf("scheduler: next due time: %w", err)
	}
	delay := s.pollInterval
	if next.Valid {
		until := time.Until(time.UnixMilli(next.Int64))
		if until <= 0 {
			delay = 10 * time.Millisecond
		} else if until < delay {
			delay = until
		}
	}
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.wake:
		return nil
	case <-timer.C:
		return nil
	}
}

// Get retrieves a job, including its failed state and last error.
// Successfully completed jobs are deleted, so Get returns ErrNoSuchJob.
func (s *Scheduler) Get(ctx context.Context, id int64) (Job, error) {
	var job Job
	var runAtMS, repeatMS int64
	err := s.db.QueryRowContext(ctx, `
        SELECT id, kind, payload, run_at_ms, status, attempts, max_attempts,
               repeat_ms, COALESCE(last_error, '')
          FROM scheduled_jobs WHERE id = ?
    `, id).Scan(&job.ID, &job.Kind, &job.Payload, &runAtMS,
		&job.Status, &job.Attempts, &job.MaxAttempts, &repeatMS, &job.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoSuchJob
	}
	if err != nil {
		return Job{}, fmt.Errorf("scheduler: get job %d: %w", id, err)
	}
	job.RunAt = time.UnixMilli(runAtMS).UTC()
	job.RepeatEvery = time.Duration(repeatMS) * time.Millisecond
	return job, nil
}

// RetryFailed resets the attempt budget for manual retries of permanently failed jobs.
func (s *Scheduler) RetryFailed(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
        UPDATE scheduled_jobs
           SET status = 'pending', attempts = 0,
               run_at_ms = ?, last_error = NULL
         WHERE id = ? AND status = 'failed'
    `, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("scheduler: retry failed job %d: %w", id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFailed
	}
	s.notify()
	return nil
}
