---
status: accepted
date: 2026-11-08
decision-makers: Repository maintainer
supersedes: 0013_keep-telegram-send-cooldown-in-memory.md
---

# Remove automatic notifications

## Context

The maintainer requested removal of notifications while keeping scheduled case
tracking. The old notification worker also handled direct reply cooldowns and
invalid-token failures, so deleting it requires preserving those reply safeguards.
ADRs [0004](0004_choose-sqlite-persistence.md),
[0007](0007_choose-polling-and-status-behavior.md),
[0011](0011_move-transaction-boundaries-to-services.md) and
[0013](0013_keep-telegram-send-cooldown-in-memory.md) currently require durable
outbound work for observed changes.

## Decision

Scheduled checks continue at the six Warsaw weekday slots in ADR 0007. Scheduled
and manual observations atomically refresh the cache and append changed stages to
history, preserving A → B → A. Neither observation source creates outbound work.
Users receive results by requesting `/status` under existing freshness, cooldown
and in-flight sharing rules. Enrollment and removal still receive direct replies.

Remove the notification worker, retry/recovery policy, recipient gates, membership
callbacks, delivery repositories and recipient recovery script. Add sequential
schema migration 2 to drop `pending_recipient`, `pending` and `recipients`.
Discard every legacy delivery, including delivered, reserved and paused records;
never replay them. Preserve enrollments, credentials, baseline/cache, history and
check cooldowns. Keep migration 1 immutable so version 1 databases can upgrade.

Retain direct replies through `internal/services/replies`: serialize one bounded
send attempt across recipients, with the existing 15-second default timeout.
Rate limits set an in-memory deadline from completion plus positive `retry_after`,
or 30 seconds otherwise. Preserve observed limits after caller cancellation;
admit sends at deadline equality. Restart clears the deadline. Replies have no
persistence or application retry. Invalid tokens close outbound admission and
stop the runtime under [ADR 0012](0012_fail-fast-on-invalid-telegram-token.md).

This supersedes ADR 0013, including its notification retry and retention policy,
and replaces notification-specific requirements in ADRs 0004, 0007, 0011, 0012,
0015, 0016 and 0017. Their tracking, ownership, transaction, scheduling, lifecycle and
external token validation requirements remain in force. No new dependencies,
configuration flags, schedule changes or live deployment are required.

## Alternatives

Disabling delivery alone would avoid automatic messages but keep accumulating
unused queue records and retain dead recovery machinery. A configurable opt-in
would preserve notifications for future use but retain the subsystem and add a
product mode the maintainer did not request. Remove the subsystem to match the
requested behavior and avoid unbounded unused delivery state.

## Consequences

Users must request results; the bot no longer alerts them to changes. Historical
case transitions remain available in storage. Notification delivery metadata is
irreversibly discarded during migration. Old binaries must not run against the
migrated database. Command reply rate limits and token failures remain protected
without any delivery background loop. No backup or downgrade tooling is added.

## Implementation plan

- Remove `internal/services/notifications`, delivery/recipient repositories and
  `scripts/resume-recipient.sql`; trim their models and Store bindings.
- Update `tracking.Persistence.Observe` to commit only cache/history and remove
  the coordinator's unused notification activity gate. Retain check/removal drains.
- Add `internal/services/replies` from the former direct-send path; wire it in
  `internal/application`, supervising only Telegram intake and scheduling.
- Remove membership recovery from `internal/entrypoints/telegram`; update help
  and removal text to describe silent tracking and retained on-demand status.
- Add `internal/repositories/migrations/000002_remove_notifications.up.sql`.
  Run it through normal startup migration under the existing database lock.
- Replace notification tests with migration preservation, silent scheduled checks,
  on-demand status and reply cooldown/token tests. Update maintained guides and
  append dated verification evidence without rewriting previous audit results.

## Verification

- [x] Scheduled checks persist changes without Telegram sends; `/status` replies
  with the resulting cached status.
- [x] Manual and scheduled A → B → A retain two history transitions.
- [x] A version 1 database with a reserved delivery upgrades without losing
  enrollment, credentials, cached observation, history or check cooldowns.
- [x] Migration removes the queue, recipient gates and queue trigger.
- [x] Direct replies honor cooldown boundaries after cancellation and close
  admission after invalid-token rejection.
- [x] `go test ./...`, `go test -race ./...`, `go vet ./...`, root build and
  documentation link/format checks pass. Live acceptance remains separate.

## Verification evidence — 2026-11-08

All listed offline checks passed with Go 1.27.3. See the dated command results
and environment limitations in [implementation progress](../implementation-progress.md).
No live deployment or external-service acceptance is claimed.

### 2026-11-08: Simplify remaining delivery-era contracts

History insertion now returns only an error; callers no longer need the inserted
sequence to enqueue delivery work. Removal drains its single active check without
a collection of delivery activities. Removed unused storage errors/time conversion
and delivery message IDs. Telegram send outcomes retain only accepted, failed,
rate-limited and invalid-token categories; blocking/permanent/transient distinctions
no longer affect direct replies. Reply serialization and the runtime supervisor
remain necessary for asynchronous enrollment replies and shutdown coordination.

### 2026-11-08: Direct case service and application wiring

[ADR 0019](0019_simplify-application-structure.md) removes the Coordinator,
Persistence adapter, monitoring decorator and runtime object. cases.Service owns
transactions directly. The bot and scheduler live in direct internal packages;
reply handling is private to the bot. The checked-in initial schema already
excludes notifications; this structural refactor makes no schema change.
