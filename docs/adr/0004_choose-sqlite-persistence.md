---
status: accepted
date: 2026-10-31
decision-makers: Repository maintainer
---

# Choose SQLite with modernc for case state and pending notifications

## Context and Problem Statement

Case Watch must remember the last successful case stage across restarts, retain observed change history, and recover notifications that have not been confirmed sent. The initial service has two users and one instance on a Raspberry Pi 5 with 4 GB RAM running Linux ARM64. The existing Go portal probe returns a stage but has no persistence.

[ADR 0002](0002_choose-go.md) selects Go and direct portal RPC. [ADR 0003](0003_choose-telegram-bot-integration.md) selects the Telegram library and long polling, explicitly leaving application delivery guarantees open. Neither selects storage.

The maintainer agreed to SQLite and selected modernc for build and deployment simplicity. Docker remains an available option, not a required deployment method. Possible duplicate notifications are preferable to silently losing an observed transition.

## Decision Drivers

- Atomically persist current stage, change history, and pending notifications.
- Recover pending work after restart without requiring a database server.
- Keep direct Linux ARM64 builds straightforward, without a C toolchain.
- Favor maintainability and inspection tools over speculative throughput optimization.
- Make no unmeasured claims about RAM, CPU, or executable size.

## Considered Options

- JSON snapshot with atomic file replacement.
- bbolt transactional key/value storage.
- SQLite using `mattn/go-sqlite3` through CGO.
- SQLite using `modernc.org/sqlite`.
- Specialized SQLite interfaces: `ncruces/go-sqlite3` and `zombiezen.com/go/sqlite`.

## Decision Outcome

Use embedded SQLite through `modernc.org/sqlite` and Go's `database/sql`. Use handwritten parameterized SQL initially; no ORM or query generator is selected. Pin the reviewed driver version v1.61.1 when implementing storage, commit its dependency checksums, and review later upgrades explicitly.

SQLite runs inside the bot process and stores data on local disk. It requires no database server. The modernc driver avoids CGO, allowing Go-only builds and cross-compilation. This convenience is the reason for the driver choice, rather than a claim of lower memory use or greater reliability than SQLite's C implementation.

### Persistence contract

- Store case identity, recipient association, latest successful stage, observed stage transitions, and pending notification records. Keep portal passwords, encryption, and key management outside this decision.
- The first successful observation establishes a baseline. Confirm tracking separately; do not fabricate a stage-change event for the baseline.
- A subsequent successful observation with a different stage atomically updates the latest stage, appends history, and creates pending notifications for the associated recipients. Compare and update within one transaction so concurrent observations of the same transition do not duplicate it.
- An unchanged stage creates no history transition or notification. Failed portal checks preserve the last successful stage.
- Record observation timestamps; they are not the government's actual transition timestamps. Retain transition history initially, rather than every unchanged check.
- Identify transitions by their occurrence, not merely their stage text. A sequence A → B → A must retain both transitions and pending notifications even if earlier sends have not completed.
- Send outside the database transaction, then persist Telegram's successful acceptance. Recover pending records after restart. If acceptance is uncertain or the process crashes before recording success, a retry may duplicate a message.
- Persist pending work before attempting delivery. A storage failure must not produce a notification whose transition was not committed. Permanent delivery failures must remain distinguishable from successful delivery; detailed retry and terminal-failure policies belong to notification implementation.

This contract supports recoverable attempts, not exactly-once delivery or guaranteed delivery to unavailable recipients. It does not make Telegram command processing durable or allow the bot to observe portal changes that occur between checks.

### Operations and data ownership

Keep SQL and driver types inside a future `internal/storage/` package. Domain operations use ordinary Go values and contexts. Transactions must not span portal or Telegram network requests.

Use one local database file configured by `CASE_WATCH_DB_PATH`, in a directory writable by the service and excluded from version control. Begin with serialized database access through one `database/sql` connection. Do not disable journal or synchronization protections to optimize this small workload. Validate the concrete SQLite settings in the implementation tests.

Use ordered, versioned SQL migrations with a stored schema version and transactional upgrades; reject an unsupported newer schema. There is no existing persistent dataset to migrate. Back up before upgrades. Initially, stop the service and close the database cleanly before copying it to a separate backup location; restore with the service stopped and validate the restored schema and pending records before resuming. Do not treat copying an actively written database file as a consistent backup. Pending work restored from an older backup can cause duplicate notifications.

### Consequences

- SQL transactions and inspection tools reduce custom persistence and recovery code compared with a file snapshot or key/value model.
- The application must maintain migrations and an explicit notification lifecycle, including the accepted duplicate window.
- Modernc adds generated SQLite code and supporting dependencies to the executable. Resource costs remain unmeasured for this application.
- Avoiding CGO simplifies direct builds; it does not preclude using Docker for builds or deployment.
- Durable commits still depend on filesystem and hardware behavior. Backups address recovery separately; no driver guarantees survival of arbitrary SD-card or power failures.

## Scope and Non-goals

This record selects storage and defines the comparison/recovery contract. It does not implement storage, choose credential protection, require containers, define polling intervals, or resolve durable incoming-command handling. Concrete schemas, SQL settings, and notification retry timing are implementation work governed by this contract. Existing ADRs remain accepted and are not superseded.

## Implementation Plan

These are follow-up tasks, not completed application functionality.

- [ ] Add `modernc.org/sqlite` v1.61.1 to `go.mod` and commit the resulting `go.sum`; no dependency is added by this documentation change.
- [x] Implement the storage boundary and embedded versioned migrations under `internal/storage/`, including atomic observation recording and pending-notification acknowledgement.
- [ ] Add runtime database-path configuration and ignore local database artifacts; document initialization, backup, restore, and upgrade procedures.
- [x] Integrate callers so successful observations use the same comparison transaction and notification sends happen only after commit.
- [x] Implement offline tests using temporary databases and synthetic case/recipient data, including reopen and crash-boundary scenarios.
- [ ] Verify Linux ARM64 builds with CGO disabled. Measure executable size, process memory, and workload cost on the target Pi before claiming resource suitability.

### Verification

- [x] First observation creates a baseline without a change notification; unchanged checks create neither transitions nor notifications.
- [x] A transition commits latest stage, history, and pending recipient records together; rollback leaves none of those partially updated.
- [x] A → B → A preserves both transitions and pending notifications.
- [x] Concurrent identical observations yield one recorded transition; failed portal checks preserve state.
- [x] Reopening before/after transaction commit demonstrates rollback or complete recovery, respectively.
- [x] A crash after commit leaves pending work; a crash after send but before acknowledgement leaves retryable work with a documented possible duplicate.
- [x] Storage failure prevents an unrecorded notification, and confirmed delivery survives restart.
- [ ] Migration and backup/restore tests preserve history and pending records and reject unsupported schema versions.
- [x] Offline tests require no Telegram or government-portal access; Linux ARM64 compilation succeeds with `CGO_ENABLED=0`.

## Pros and Cons of the Options

### JSON snapshot

Simple to inspect and requires no database dependency. Not chosen because locking, durable replacement, recovery, history queries, and schema evolution would become application-maintained persistence machinery.

### bbolt

Provides embedded transactions and a Go key/value API without CGO. Not chosen because the application would manage relationships, encoding, and query indexes that SQL expresses directly.

### SQLite with mattn

Uses SQLite's C implementation, has broad adoption, and supports `database/sql`. It remains a strong alternative. CGO requires a suitable C toolchain for each build target; Docker can provide that environment but is not mandatory for this project. Not chosen because the maintainer prioritizes simple builds both inside and outside containers. No measured memory disadvantage is asserted.

### SQLite with modernc

Provides SQLite transactions and standard SQL tooling without a C build toolchain. Chosen for build/deployment flexibility and the relational fit. Its translated implementation, dependencies, and resource footprint still require version review and measurement.

### Specialized SQLite interfaces

Ncruces offers a CGO-free alternative but has a smaller observed adoption base and no established advantage for this workload. Zombiezen offers explicit SQLite APIs over modernc but deliberately omits `database/sql`. Neither adds a required capability that justifies departing from the selected interface and driver.

## More Information

Primary sources reviewed on 2026-10-31:

- [Modernc driver documentation and supported platforms](https://pkg.go.dev/modernc.org/sqlite).
- [SQLite atomic commit and hardware assumptions](https://www.sqlite.org/atomiccommit.html).
- [SQLite backup documentation](https://www.sqlite.org/backup.html).
- [Mattn build requirements](https://github.com/mattn/go-sqlite3).
- [bbolt transactions and operational guidance](https://github.com/etcd-io/bbolt).
- [Ncruces documentation](https://github.com/ncruces/go-sqlite3).
- [Zombiezen documentation](https://github.com/zombiezen/go-sqlite).

Revisit the driver if maintenance, platform compatibility, or measured resource use becomes a problem. Revisit local SQLite if deployment requires independent writers on multiple hosts. No application benchmark or live-service test was performed for this documentation decision.

### 2026-11-01: Backup requirements deferred

[ADR 0008](0008_choose-container-deployment-without-backups.md) explicitly defers backup creation, pre-upgrade backups, and backup/restore verification for the initial personal deployment. The maintainer accepts local data loss and re-enrollment. The backup procedure above remains historical guidance for future adoption, not an initial implementation requirement. Transactions, versioned migrations, schema checks, and the other storage requirements remain active.

### 2026-11-01: Explicit owner-requested removal exception

The confirmed [product contract](../product-contract.md) permits deletion of an owner's case credentials, baseline/history and pending notifications only after an explicit, unexpired `/remove` confirmation. Cancel/drain that enrollment's checks and sends and fence late results so deletion cannot resurrect data or affect re-enrollment. Preserve user cooldowns and recipient/global delivery gates. This scoped exception is deliberate user-requested deletion, not silent notification loss: authorization suppression, unsuccessful sends, retry exhaustion and restart must still retain undelivered transitions under the policy above. Later storage/coordinator tests must verify atomic removal, late-result rejection and gate retention with synthetic data.

### 2026-11-01: Independent implementation evidence

`internal/storage` implements transactional v1/v2 migrations, serialized durable SQLite settings, owner/identity predicates, atomic baseline/observations, history and ordered pending state. Fault/reopen/concurrency tests cover atomic rollback, occurrences, delivery uncertainty, confirmed outcomes and future-schema refusal. Runtime configuration and upgrade/loss guidance exist in `docs/storage.md` and `docs/operations.md`; composite historical backup items remain unchecked because backup/restore is deferred by ADR 0008, rather than claimed tested. Linux ARM64 builds disable CGO; target-Pi measurements remain pending. Dependency/checksum files exist without an authorized commit.

Checklist evidence maps to the reviewed implementation and offline tests in [the independent audit](../implementation-audit.md) and exact command results in [implementation progress](../implementation-progress.md). Unchecked composite items retain their pending, conditional or explicitly deferred clauses; acceptance of this ADR is not a claim of live, Docker or Pi validation.

### 2026-11-02: Data access package and repositories

The maintainer moved the SQLite boundary from `internal/storage` to `internal/data`. Persisted-value models live in `models.go`; repository types own logical entities in the same package, and a transaction service coordinates multi-entity writes. This amends the package-path instruction above while retaining the SQLite, transaction, ownership, and network-boundary decisions. Earlier path references remain historical evidence.


### 2026-11-02: Soft-delete enrollments instead of a separate identity registry

The maintainer selected soft deletion for this WIP application; no production database needs migration. Replace `enrollment_ids` with retained `enrollments` rows marked by `deleted_at`. This amends the physical enrollment deletion described above, while preserving the confirmed removal contract: clear credential metadata, encrypted payload and baseline fields to SQL NULL; delete case guards and history with cascading pending deletion in the same transaction. Retain the ID, owner, deletion timestamp, owner cooldown and recipient/global gates. Restoration is outside scope.

Use a partial unique index on `owner_id` for active rows. Every enrollment read, observation, check reservation/eligibility query and credential rewrite must exclude deleted rows. Reject reuse of any enrollment ID, including deleted ones. Re-enrollment allocates a fresh identity. This consolidates active and retired identities in one table, at the cost of explicit active-row predicates and transactional dependent-row cleanup instead of relying on enrollment deletion cascades.

Implementation affects `internal/data/migrations/000001_initial.up.sql`, enrollment/history/check repositories, `transactions.go`, removal/rollback tests and maintained storage/product guides. Revise the initial migration directly for this pre-production change; earlier development databases require recreation rather than upgrade. Verify removal persistence after reopen, scrubbed fields, active-owner uniqueness, late-result rejection, identity non-reuse, rotation exclusion, rollback and retained gates using offline tests, then run the full test suite, race detector, vet and application build. See the dated evidence in [implementation progress](../implementation-progress.md).

### 2026-11-02: Service-owned transaction boundaries

Accepted [ADR 0011](0011_move-transaction-boundaries-to-services.md) replaces the earlier data-layer transaction-service coordination boundary. Entity repositories use DBTX; application services compose their operations through Store.RunInTx. SQLite, connection serialization, atomicity, ownership, soft deletion, and network-boundary requirements remain active.

### 2026-11-08: Automatic notifications removed

[ADR 0018](0018_remove-automatic-notifications.md) replaces notification-specific
requirements in this record. Scheduled and manual tracking keep cache/history
without enqueueing outbound work. The runtime now supervises Telegram intake and
the scheduler; direct replies retain rate limits and invalid-token shutdown.
Migration 2 discards legacy delivery state while preserving tracking data.
