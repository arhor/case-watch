# SQLite data access

`internal/data/models.go` defines persistence values. Entity repositories own SQL for enrollments, history, pending deliveries, check guards, recipients, and global gates. Application services compose these repositories and choose transaction boundaries under [ADR 0011](adr/0011_move-transaction-boundaries-to-services.md). `Store` owns the single SQLite connection, migrations, and transaction lifecycle.

## Repositories and transactions

The Store exposes named `Enrollments`, `History`, `Pending`, `Guards`, `Recipients`, and `Gates` repositories. They use a small `DBTX` interface implemented by both `*sql.DB` and `*sql.Tx`. Repository methods do not begin, commit, or roll back transactions. They validate persistence inputs, enforce identity predicates, and return sanitized errors without driver details or supplied data.

`Store.RunInTx(ctx, func(tx *data.Store) error) error` binds every repository on the callback Store to one transaction. Success commits; callback errors roll back and retain their identity. Panics roll back and propagate. Begin/commit failures return `data.ErrStorage`. Nested calls and closing a transaction Store return `data.ErrTransaction`. Only the root Store owns connection lifetime.

Use only the callback Store inside a transaction. Calling the root Store can deadlock because the pool has one connection. Repository handles must not escape the callback or be used asynchronously. Independent single-statement writes can use the root repositories directly. Read/modify/write operations need a service transaction even within one entity.

`tracking.Persistence` implements atomic enrollment, observation, removal, check reservations, and eligibility for the tracking coordinator. `notifications.Persistence` implements delivery reservation, completion, recovery, recipient recovery, and gate operations for the notification worker. `internal/service/maintenance.go` composes credential rewrites and operator recovery. The former `data.Transactions` and repository compositions are removed. Portal and Telegram calls occur outside database transactions; credential rotation can perform local cryptography inside its rewrite transaction.

## Storage foundation

`internal/data` implements ADRs 0004–0006 using pinned `modernc.org/sqlite` v1.61.1 and `database/sql`. `Open` requires an existing service-owned parent directory and opens one serialized connection with foreign keys, DELETE rollback journaling, FULL synchronization and a 5-second busy timeout. Keep the configured database path on local persistent storage and restrict its directory to the service account. Database files and journals are excluded from Git. SQLite metadata, stages and owner IDs remain readable; only credentials are encrypted.

Fresh databases initialize through golang-migrate v4.20.1 and embedded SQL. The consolidated initial schema is version 1 in `schema_migrations`; `PRAGMA user_version` remains zero. Legacy databases are rejected without conversion or reset. Dirty, malformed and unsupported metadata prevent startup. The 2026-11-02 WIP soft-deletion change revises the initial schema directly; earlier version-1 development databases must be recreated and have no upgrade path. For subsequent changes, add immutable sequential migrations under `internal/data/migrations` as `.up.sql` files. Each SQL migration is transactional, while version/dirty markers commit separately. No downgrade command or automatic force is provided. Backups remain deferred under ADR 0008. The service transaction refactor does not change the schema.

Allocate an opaque `NewEnrollmentID`, seal transient credentials using the credential adapter and that ID, then enroll through the tracking service with the authenticated owner and successful portal baseline. One owner can have one active enrollment. The credential ID, ciphertext and baseline commit together, with no baseline notification. A partial unique index restricts active owners; retained soft-deleted rows prevent identity reuse. Read methods reconstruct the credential ID from its destination row for authenticated encryption binding. Startup authenticates all active records before networking; data repositories do not hold encryption keys or accept plaintext credentials.

Observation orchestration reads the active owner/identity, compares stages, updates cache/time, and inserts history and durable pending work in one transaction. Unchanged stages refresh only successful-check time. Transitions are individual occurrences with increasing sequence IDs; A → B → A retains both changes and queued records. The recipient is the enrollment owner. A failure leaves the earlier cache and queue unchanged.

Enrollment reads are owner-scoped. Enrollment inventory is trusted startup/background input: consumers authorize before decryption or work. History requires owner and identity. No SQL or driver values escape these repository APIs. Owning a row does not authorize work.

## Checks and removal

The tracking service atomically reserves case and optional manual user attempt-start guards, with eligibility at exactly 60 seconds. Failed checks consume reservations; cache reads and joins skip reservation. Eligibility reads the stored start times without writing guards. The coordinator owns in-process joining and successful-cache freshness, and prevents overlapping checks even if one lasts over 60 seconds.

Confirmed removal cancels and drains checks/sends before its transaction. It sets `deleted_at`, clears credential and baseline fields to SQL NULL, and deletes case guards and history, cascading deletion to the queue. It retains enrollment ID/owner, user manual guard, recipient pauses and global gates. Active reads, observations, check reservations and credential rotation exclude deleted rows. Re-enrollment uses a fresh identity; restoration is unsupported. Late observations and delivery acknowledgements fail against removed identities.

## Delivery and recovery

The pending repository exposes queue metadata in enqueue order, including delivered rows and message IDs, and inventories the oldest outstanding item per recipient. The notification service reserves only the oldest outstanding record, increments cycle/lifetime counts before network access, and enforces recipient pauses, token suspension and rate deadlines. The sequence/enrollment/lifetime tuple is the completion token; stale completions cannot acknowledge newer attempts.

Completion commits the sanitized outcome, backoff, message ID and gates together. Failure delays are 30/60/120/240/480 seconds from completion, with rate-limit minimums. The sixth failure retains the head and pauses the recipient; later records cannot overtake it. Authorization suppression occurs before reservation without consuming an attempt. A reserved attempt that provably never reached the network can be released with its exact token, restoring counters; never release after a send or uncertain acceptance.

Startup recovers reservations once before workers start. Interrupted attempts become `uncertain` failures, retaining counts and applying backoff from recovery time or pausing an exhausted cycle. Never recover while sends are active. Completion persistence failures stop the worker; restart recovery handles the uncertainty. A send accepted before acknowledgement may be duplicated after recovery.

Operator recipient recovery resets only the oldest outstanding cycle/deadline and recipient pause, preserving lifetime counts and global gates. Block evidence pauses a recipient. An ordinary interaction clears only an evidenced block; explicit authenticated unblock evidence can resume even if the prior block event was missed. Recipient recovery rejects active reservations. The worker serializes membership changes with recipient sends and retains interrupted events for retry. Token recovery clears suspension while retaining rate deadlines. Direct-reply failures update global gates without acknowledging queued work. Automatic unblock recovery is exposed through the worker; operator recipient recovery uses the stopped-service [SQL script](../scripts/resume-recipient.sql). Token recovery uses its maintenance command. These are not Telegram administrator commands.

## Verification

Offline tests use temporary databases and synthetic credentials. Data tests cover transaction lifecycle, durability settings, and migrations. Service integration tests cover rollback through injected SQL failures, repeated stages, concurrent observations/reservations, removal, reopen, ownership, ciphertext binding and plaintext exclusion. Tracking, notification and rotation tests cover caller behavior and interruption recovery. Local HTTP fixtures simulate network adapters; live portal/Telegram and deployment acceptance remain separate. Dated command evidence is appended to [implementation progress](implementation-progress.md).
