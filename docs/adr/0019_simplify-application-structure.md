---
status: accepted
date: 2026-11-08
decision-makers: Repository maintainer
supersedes: 0016_encapsulate-application-bootstrap.md
---

# Simplify application structure

## Context

The maintainer requested a global refactor around a simple Telegram bot, scheduled
case checks, a few repositories/services and an application that constructs and
launches them. The existing Coordinator, Persistence adapter, monitoring decorator,
reply service and runtime owner make that small application difficult to follow.
The request authorizes replacing those structural choices while retaining behavior.

## Decision

Use `internal/services/cases.Service` as the case service. It owns enrollment,
status checks, removal, shared checks and repository transactions directly. Remove
the intermediate Store interface and Persistence adapter. Keep SQL in the existing
repositories and compose transactions through `Store.RunInTx`.

Place the bot in `internal/bot` and the scheduled job in `internal/scheduler`.
Each declares only the case methods it consumes. Remove the shared domain.Tracking
interface. Keep domain values and the small Portal, Clock and Sender collaborators.
Reply serialization and rate limits are private bot implementation details.

Put construction and launching together in `internal/application/application.go`.
Use `context.WithCancelCause` for failure cancellation, two goroutines for the bot
and scheduler, a wait group for joining them and ordinary defers for cleanup.
Remove the runtime object, failure latch, wiring file and forwarding monitor.
The case service reports storage failures through a constructor callback.
`main.go` continues to call only `application.Run(ctx)`.

Preserve private ownership, credential handling, enrollment validation, cache and
history transactions, cooldowns, shared check results, removal cancellation,
Warsaw weekday slots, invalid-token shutdown and silent scheduled checks. The
current consolidated initial schema remains unchanged; there is no migration 2
in this checkout and no upgrade/reset operation is introduced by this refactor.

This replaces ADR 0016's implementation structure. ADRs 0011 and 0015 retain
service-owned transactions and service/repository responsibilities, with the
simpler packages and direct dependencies above. ADR 0018's separate reply-service
placement is replaced by bot-private reply handling. The explicitly protected
experimental bot remains untouched.

## Alternatives and consequences

Renaming Coordinator alone would leave the same forwarding layers. Moving all
logic into Telegram handlers would duplicate manual/scheduled behavior and make
removal races harder to control. A concrete case service removes the indirection
while retaining one place for shared behavior. Service tests use temporary SQLite
databases and injected SQL failures instead of mocking an intermediate adapter.

The repository schema currently already excludes notification tables. Replace the
stale migration test with initialization/reopen coverage for that actual schema;
update maintained guides accordingly. No dependency, configuration, product behavior
or deployment change is required.

## Implementation and verification

- [x] Replace tracking Coordinator/Persistence with cases.Service and private
  transaction methods; remove unused forwarding contracts and utility package.
- [x] Move bot/scheduler to direct packages; keep replies private to the bot.
- [x] Consolidate application construction/launch and remove runtime/monitor wrappers.
- [x] Adapt behavior tests, using actual repository failures; retain startup cleanup,
  cancellation, token/storage failures, status sharing, removal and scheduling coverage.
- [x] Run uncached full tests, race tests, vet, root build and documentation checks.

The current structure is summarized in [architecture](../architecture.md).
Verification evidence is appended to [implementation progress](../implementation-progress.md).

## Verification evidence — 2026-11-08

Uncached ordinary/race suites, vet, root build and changed-document link/whitespace
checks passed with Go 1.27.3. Temporary SQLite fixtures exercise storage failures
through the actual case service. See [implementation progress](../implementation-progress.md)
for commands and limits. No live deployment or external-service acceptance is claimed.
