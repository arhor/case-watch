---
status: accepted
date: 2026-11-02
decision-makers: Repository maintainer
---

# Move transaction boundaries to services

## Context and Problem Statement

The maintainer wants granular repositories and explicit service-owned transactions using a `Store.RunInTx` callback. Today `internal/data/transactions.go` combines SQL, use-case decisions, and transaction lifecycle management. Check reservations and recipient recovery also open transactions inside their repositories. The `Store` embeds these operations and exposes focused compositions to tracking and notifications.

[ADR 0004](0004_choose-sqlite-persistence.md), including its 2026-11-02 amendments, requires atomic observations, removal, durable delivery state, and identity fencing. Its repository amendment assigns coordination to a transaction service in `internal/data`. This decision replaces that coordination boundary while retaining its storage and behavior contracts. ADR 0004 remains accepted for its other requirements.

## Decision Drivers

- Express use-case sequencing and transaction scope in application services.
- Keep SQL, scanning, driver types, and sanitized persistence errors in `internal/data`.
- Reuse the same repositories with either a database or a transaction.
- Preserve existing concurrency, rollback, recovery, and removal guarantees.
- Use the existing standard library and SQLite dependencies.

## Considered Options

- Keep `data.Transactions` and repository-owned transactions.
- Bind repositories through `Store.RunInTx` and coordinate them in services.
- Introduce a separate unit-of-work or transaction-manager abstraction.

## Decision Outcome

Use `Store.RunInTx(ctx, func(tx *Store) error) error`. The Store owns the database connection, migrations, repository construction, and transaction lifecycle. Application services own when to open a transaction and which repository operations to execute within it. `tracking.Coordinator` and `notifications.Worker` are application services despite their package names; maintenance orchestration remains in `internal/service`.

### Data boundary

- Define a small `DBTX` interface in `internal/data` with `ExecContext`, `QueryContext`, and `QueryRowContext`, implemented by both `*sql.DB` and `*sql.Tx`. QueryContext is required by inventory and recovery reads.
- Give entity repositories a `DBTX` handle. Expose them as named Store fields for enrollment, history, pending deliveries, check guards, recipients, and global gates. Avoid embedding methods into a global use-case API.
- Repository methods perform focused persistence operations: reads, inserts, updates, and deletes. They may enforce row ownership, active-enrollment predicates, uniqueness, and conditional attempt-token checks in SQL. They never begin, commit, or roll back transactions.
- Services compose those operations. Observation comparison, retry/backoff decisions, eligibility decisions, recovery sequencing, and multi-entity cleanup belong to services. Keep SQL out of services; expose the needed reads and writes rather than copying SQL into a callback.
- Remove `data.Transactions` and its focused compositions. Update consumer-owned contracts and wiring to use the new boundary. Do not retain forwarding methods that continue to hide workflow orchestration in `data`.

### Transaction contract

- `RunInTx` begins one transaction using the supplied context and default transaction options, creates a Store whose repositories all use that transaction, and calls the callback synchronously.
- A successful callback commits. A callback error rolls back and preserves the callback error. Begin and commit failures return a sanitized storage error. Defer rollback immediately after beginning; a panic rolls back while propagating normally.
- A transaction-bound Store rejects nested `RunInTx` before invoking its callback, using a stable error. Do not silently join a transaction, start another one, or implement savepoints.
- The callback uses only its transaction-bound Store. Repository handles must not escape the callback or be used asynchronously. Calling the root Store from the callback can deadlock with the existing single-connection pool.
- Transaction Stores do not own the connection or migrations. Reject `Close` on a transaction Store without closing the root database; only the root Store controls connection lifetime.
- Use ordinary repository calls for independent single-statement operations. Read/modify/write workflows require a service transaction even when all affected rows belong to one repository.

### Preserved behavior

Keep baseline enrollment, stage comparison/cache/history/pending insertion, soft deletion with credential scrubbing and dependent cleanup, check reservations, delivery reservations/completions/recovery, and recipient recovery atomic at their existing boundaries. Keep immutable enrollment and attempt-token predicates. Preserve cooldowns, recipient/global gates, retry limits, ordered delivery, error categories, and credential-rotation interruption recovery.

Portal and Telegram calls remain outside transactions. Rotation may perform local cryptography inside its database transaction; persist keyring files outside that transaction in the existing interruption-safe order. Recovery closes inventory rows before issuing writes on the same transaction.

One Store covers the current application. It constructs all six lightweight repositories eagerly. Separate module Stores, ambient context transactions, additional transaction frameworks, schema changes, and product changes are outside scope.

### Consequences

- Services can compose new atomic workflows without adding another data-layer use-case method.
- The Store explicitly couples repository construction to the application’s persistence boundary; adding a repository requires updating both ordinary and transaction-bound construction.
- Services acquire a dependency on the Store callback contract and need transaction-aware test fixtures. Consumer interfaces should stay focused where useful, without introducing another transaction framework.
- Granularity exposes more repository methods and makes correct transaction usage a caller responsibility. Tests must exercise rollback and concurrency through services, rather than assuming separate statements remain atomic.
- Existing direct Store callers and tests must migrate together. No database migration, configuration change, or dependency change is required.

## Implementation Plan

1. In `internal/data/database.go` and a focused transaction infrastructure file, add DBTX, named repository fields, shared repository construction, and `RunInTx`. Keep Open/migrations/Close on the root Store. Add a reference to this ADR at the transaction entry point.
2. Refactor `internal/data/*_repository.go` to use DBTX and expose the focused operations currently buried in `transactions.go`, check reservation, and recipient recovery. Keep validations, scanners, SQL, and error sanitization in data. Remove workflow orchestration and all repository transaction lifecycle calls.
3. Move tracking workflows into `internal/tracking`, delivery/recovery workflows into `internal/notifications`, and rotation/maintenance transaction composition into `internal/service/maintenance.go`. Use small service helper files where needed; helpers must not hide SQL or perform network calls inside transactions.
4. Update `internal/application/contracts.go`, consumer Store contracts, and `internal/service/service.go` wiring. Remove obsolete `application.Store` atomic-write assertions and `TrackingRepositories`/`NotificationRepositories` compositions as their callers migrate. Keep existing transport-facing behavior.
5. Migrate `internal/data/*_test.go` persistence checks to granular repositories. Move workflow assertions to tracking, notifications, and service tests; update scheduler and Telegram fixtures that currently call atomic Store methods. Retain fault injection, temporary-database reopen, and concurrency coverage.
6. Update `docs/storage.md` to describe actual repository and service ownership after implementation. Update operations/credentials guides only where their descriptions change. Append dated implementation verification evidence to `docs/implementation-progress.md`, preserving historical results.

### Verification

- [x] Repository fields use DBTX; transaction lifecycle calls for application writes occur only in Store.RunInTx. Migration infrastructure retains its separate transaction lifecycle.
- [x] SQL and database/sql types remain inside data; data no longer implements application workflow orchestration or retry policy.
- [x] RunInTx commits changes across multiple repositories on success and rolls back all changes on callback error, cancellation, or panic; callback errors and panics retain their identity.
- [x] Nested RunInTx rejects without invoking the nested callback; transaction Store.Close cannot close the root connection; root repositories remain usable after either rejection.
- [x] Baseline and unchanged checks produce no history or pending delivery; concurrent identical observations produce one transition; A → B → A preserves both occurrences.
- [x] Faults during observation or removal roll back every affected entity. Removal retains its scrubbed identity, owner cooldowns, and delivery gates; late observations and acknowledgements cannot affect re-enrollment.
- [x] Concurrent check/delivery reservations do not admit duplicate work. Stale attempt tokens fail; recovery, release, six-attempt limits, backoff, membership recovery, and global gates retain existing behavior after reopen.
- [x] Credential rewrite failure rolls back all rewritten records; soft-deleted enrollments remain excluded; keyring interruption tests retain the existing recovery guarantees.
- [x] Portal and Telegram calls occur outside RunInTx callbacks, and no transaction-bound repositories escape callbacks.
- [x] After selecting the repository Node version with nvm use, run Go 1.27.3 checks: go test ./..., go test -race ./..., go vet ./..., and go build -o /tmp/case-watch-adr-0011 ./cmd/case-watch. Record actual results and any environment limitations.
- [x] Documentation links resolve and git diff --check passes. Offline verification is reported separately from live Telegram/portal and deployment acceptance.

## Pros and Cons of the Options

Keeping the current transaction service minimizes caller changes and gives consumers compact atomic methods. It retains use-case orchestration in the data layer and requires new composite data APIs as workflows evolve.

Store.RunInTx matches the maintainer’s requested design and makes transaction scope explicit where the use case executes. Its costs are the larger focused repository API, caller discipline, and coordinated migration of tests and consumers.

A separate unit-of-work or transaction manager could decouple transaction contracts from the concrete Store. The current application already has one Store and one database; another lifecycle abstraction adds indirection without a required capability.

## More Information

The maintainer approved this record on 2026-11-02, after requesting the RunInTx method name. Revisit the single Store boundary if independently owned modules or transactions spanning multiple databases become necessary.

### 2026-11-02: Implementation verification

Implemented DBTX repositories, Store.RunInTx, tracking/notification service workflows, and service-owned credential rewrites. Workflow fault/reopen/concurrency tests now run in the service package; data tests cover transaction lifecycle and migration/connection behavior. Final full-suite tests, race tests, vet, and the native application build passed with Go 1.27.3. See the dated [implementation evidence](../implementation-progress.md). Live endpoints and deployment were not exercised.
