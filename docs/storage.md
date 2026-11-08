# SQLite data access

`internal/repositories/models.go` defines persistence values. Entity repositories own SQL for enrollments, history and cooldowns. Services compose these repositories and choose transaction boundaries under [ADR 0011](adr/0011_move-transaction-boundaries-to-services.md) and [ADR 0015](adr/0015_adopt-three-layer-application-structure.md). `Store` owns the single SQLite connection, migrations, and transaction lifecycle.

## Repositories and transactions

The Store exposes named `Enrollments`, `History`, and `Cooldowns` repositories. They use a small `DBTX` interface implemented by both `*sql.DB` and `*sql.Tx`. Repository methods do not begin, commit, or roll back transactions. They validate persistence inputs, enforce identity predicates, and return sanitized errors without driver details or supplied data.

`Store.RunInTx(ctx, func(tx *repositories.Store) error) error` binds every repository on the callback Store to one transaction. Success commits; callback errors roll back and retain their identity. Panics roll back and propagate. Begin/commit failures return `repositories.ErrStorage`. Nested calls and closing a transaction Store return `repositories.ErrTransaction`. Only the root Store owns connection lifetime.

Use only the callback Store inside a transaction. Calling the root Store can deadlock because the pool has one connection. Repository handles must not escape the callback or be used asynchronously. Independent single-statement writes can use the root repositories directly. Read/modify/write operations need a service transaction even within one entity.

`cases.Service` calls the concrete Store repositories and composes enrollment,
observation, removal and check-reservation transactions directly. Its private
methods live in `internal/services/cases/transactions.go`; there is no persistence
adapter or intermediate repository interface. Portal and Telegram calls occur
outside transactions.

Input and enrollment-state validation belongs to Go repository methods rather than SQL `CHECK` constraints. Enrollment creation requires a valid identity, nonblank case number and password, a positive owner, and a successful observation timestamp. Enrollment methods maintain complete active records, clear all credential/baseline fields during soft deletion, and reject updates to deleted identities. SQLite retains required-column, primary-key, uniqueness and foreign-key constraints, plus atomic identity predicates. Direct SQL writes bypass Go validation and must preserve these invariants themselves.

## Storage foundation

`internal/repositories` implements SQLite persistence using pinned `modernc.org/sqlite` v1.61.1 and `database/sql`. `NewStore` opens one serialized connection with foreign keys, WAL journaling, FULL synchronization and a 5-second busy timeout after app-level path validation. Keep the configured database path on local persistent storage and restrict its directory to the service account. Database files and journals are excluded from Git. SQLite stores portal credentials and application metadata as plaintext, so access to the database file grants access to them.

Fresh databases initialize through golang-migrate v4.20.1 and embedded SQL. The consolidated initial schema is version 1 and contains enrollments, history and cooldowns. Notification tables are absent; there is no migration 2 in the current checkout. This WIP schema has no automatic conversion from earlier notification-era databases. `schema_migrations` tracks the applied version; `PRAGMA user_version` remains zero. Legacy databases are rejected without conversion or reset. Dirty, malformed and unsupported metadata prevent startup. Earlier development databases with incompatible legacy schemas have no upgrade path. Add immutable sequential migrations under `internal/repositories/migrations` as `.up.sql` files. Each SQL migration is transactional, while version/dirty markers commit separately. No downgrade command or automatic force is provided. Backups remain deferred under ADR 0008.

Allocate an opaque `NewEnrollmentID`, then enroll through the case service with the private-chat owner, plaintext portal credentials and successful portal baseline. One owner can have one active enrollment. The credentials and baseline commit together, with no baseline history transition. A partial unique index restricts active owners; retained soft-deleted rows prevent identity reuse. The identity continues to fence late work after removal and re-enrollment.

Observation orchestration reads the active owner/identity, compares stages, updates cache/time, and inserts history in one transaction. Unchanged stages refresh only successful-check time. Transitions are individual occurrences with increasing sequence IDs; A → B → A retains both changes. A failure leaves the earlier cache and history unchanged.

Enrollment reads are owner-scoped. Enrollment inventory supplies all enrolled owners to background work. History requires owner and identity. No SQL or driver values escape these repository APIs.

## Checks and removal

`cooldowns` stores `subject_type`, `subject_id`, and `started_at` (Unix nanoseconds), keyed by `(subject_type, subject_id)`. `case_check` subjects are enrollment IDs; `manual_check` subjects are canonical decimal owner IDs. Subject references have no foreign keys: the case service validates the active owner/enrollment inside its eligibility and reservation transactions. The generic `CooldownRepository` returns `Cooldown` entities through `FindBySubjectTypeAndSubjectID` (`ErrNotFound` when absent), accepts entities through `Upsert`, and deletes idempotently by subject type and subject ID. The Go repository rejects blank keys and zero write timestamps. SQLite enforces non-null columns and composite-key uniqueness; it does not validate blank cooldown keys. Types are extensible; durations and eligibility policy belong to the case service. Removal explicitly deletes only the case cooldown and retains the owner cooldown. Existing development databases using the former guard tables must be recreated explicitly; startup does not convert or reset them.

The case service atomically reserves case and optional manual user attempt-start guards, with eligibility at exactly 60 seconds. Failed checks consume reservations; cache reads and joins skip reservation. Eligibility reads the stored start times without writing guards. The case service owns in-process joining and successful-cache freshness, and prevents overlapping checks even if one lasts over 60 seconds.

Confirmed removal cancels and drains checks before its transaction. It sets `deleted_at`, clears credential and baseline fields to SQL NULL, and deletes case guards and history. It retains enrollment ID/owner and user manual guard. Active reads, observations and check reservations exclude deleted rows. Re-enrollment uses a fresh identity; restoration is unsupported. Late observations fail against removed identities.

## Verification

Offline tests use temporary databases and synthetic credentials. Data tests cover transaction lifecycle, durability settings, and migrations. Service integration tests cover rollback through injected SQL failures, repeated stages, concurrent observations/reservations, removal, reopen and ownership. Case-service tests cover caller behavior and interruption recovery. Storage tests cover initialization and reopening with preserved tracking records; reply tests cover in-memory send cooldowns and token rejection. Local HTTP fixtures simulate network adapters; live portal/Telegram and deployment acceptance remain separate. Dated command evidence is appended to [implementation progress](implementation-progress.md).
