---
status: accepted
date: 2026-11-03
decision-makers: Repository maintainer
supersedes: 0009_choose-notification-retry-policy.md
---

# Keep the Telegram send cooldown in memory

## Context and Problem Statement

ADR 0009 requires a persisted global Telegram rate-limit deadline in addition to durable notification retries. This home-hosted utility checks cases about every two hours. Preserving the shared send cooldown across restart has little value compared with removing its singleton table, repository and database calls. The maintainer explicitly accepts another API attempt after restart: if Telegram still rejects sending, the response establishes a new cooldown.

The maintainer approved implementation and direct revision of the initial schema on 2026-11-03. The service is still in development and is not deployed. This record supersedes [ADR 0009](0009_choose-notification-retry-policy.md), retaining its delivery policy below with an in-memory shared cooldown. [ADR 0012](0012_fail-fast-on-invalid-telegram-token.md) continues to govern invalid tokens. This record replaces the global-gate persistence requirements referenced by ADRs 0004 and 0011; their remaining storage and transaction decisions continue to apply.

## Decision

### Shared outbound cooldown

The notification worker owns one in-memory deadline shared by durable notification sends and direct command replies for all recipients. Serialize send admission, the transport attempt and deadline updates with the worker's existing send mutex. No database access is needed to check or extend this deadline.

On a rate-limit outcome, extend the deadline to the later of its current value and the completion time plus Telegram's positive `retry_after`. If no positive delay is supplied, use 30 seconds, the initial application retry delay. Suppress sends while the clock is before the deadline; equality permits admission. Waiting consumes no notification attempt. Record the deadline even if the caller was canceled or notification outcome persistence fails.

A fresh worker starts without a shared cooldown. Restart may make another send attempt while Telegram's limit remains active; another rejection establishes the cooldown again. Removal, re-enrollment and recipient recovery within the same process do not clear it. Polling backoff remains independent. Do not sleep in a handler or hold a database transaction while waiting.

### Durable retry and ordering policy

- Persist the enqueue sequence, notification status, cycle/lifetime attempt counts, sanitized failure category, next-attempt deadline, confirmed message ID and recipient pause in SQLite.
- Each cycle permits one initial attempt plus five retries. Retry delays from failed completion are 30/60/120/240/480 seconds without jitter. Use the greater of that delay and Telegram's positive `retry_after` for the notification's own deadline. That deadline remains durable across restart independently of the shared cooldown.
- Reserve attempts durably before network access. Failed, rate-limited and uncertain attempts consume the cycle budget. Authorization suppression, waiting and known recipient pauses consume none. Proven unsent reservations can be released using their exact attempt token, restoring counters. Explicit invalid-token rejection restores counters and stops the service under ADR 0012.
- Send the oldest outstanding transition first for each recipient with one send in flight. Later records cannot overtake a retrying or paused head. Retain all transitions, including both changes in A → B → A. Never acknowledge an unsuccessful send to advance the queue.
- Acceptance persists the returned message ID. Transient network/server failures retry. Definite blocking pauses the recipient; other permanent failures pause and report sanitized diagnostics. Use structured error information and do not classify every forbidden error as blocking.
- Six failed attempts retain the head and pause the recipient as exhausted. Restart and new observations do not reset exhausted cycles. Operator recovery resets only the oldest outstanding cycle/deadline and recipient pause, preserving lifetime attempts and ordering.
- Authenticated private unblock evidence resumes the oldest head with a fresh cycle. Ordinary interaction clears only a known block; unrelated messages cannot clear permanent/exhausted pauses. Recovery depends on observing evidence and is not guaranteed after arbitrary downtime. Current authorization and the in-memory shared cooldown still apply.
- Startup recovers interrupted reservations once before sends begin, retains their attempt count and applies backoff or exhaustion. Accepted-but-unrecorded messages may be duplicated. Complete notification outcomes and recipient state atomically in short service-owned transactions; never hold a transaction across network I/O. Outcome storage failure stops the worker for restart recovery.
- Confirmed owner removal cancels/drains work and fences late results before deleting that enrollment's credentials, observations and pending work under the product contract. It retains owner cooldowns and recipient gates; it cannot recall messages already accepted by Telegram.
- Direct replies have no application retry or durable queue. A rate-limited or suppressed reply does not acknowledge queued notifications. Diagnostic output excludes tokens, credentials, raw transport errors and message bodies.

## Alternatives and Consequences

- Keeping the deadline in SQLite avoids a possible rejected request after restart, but requires a singleton table, repository and database access during send admission. Rejected because restart continuity is unnecessary for this service.
- Delaying only the current job avoids shared state, but lets other notifications and replies send during an observed limit. Retain the conservative shared scope while simplifying its ownership and lifetime.
- In-memory ownership reduces database operations and schema/API surface. Its accepted cost is losing the shared deadline on restart; notification retry deadlines and attempt budgets remain durable.
- Adjust the existing initial migration, removing `delivery_gates`, without adding an upgrade migration. Earlier development databases should be recreated. The stopped-service queue inspection command cannot report a process-local cooldown and drops `rate_until` from its output.

No new dependency, configuration option, automatic command retry or live integration work is introduced. Revisit if coordinating multiple bot processes or preserving cooldowns across frequent restarts becomes necessary.

## Implementation Plan

1. In `internal/notifications/worker.go`, own the deadline under the existing send mutex and record rate-limit outcomes before notification outcome persistence. Remove storage-based direct gate helpers. Keep recipient checks and durable reservation/completion behavior.
2. In `internal/notifications/persistence.go`, expose recipient state directly and remove global state reads/writes. Remove the global repository, model and Store fields in `internal/data`, and the table from `migrations/000001_initial.up.sql`.
3. Remove the global deadline from `cmd/case-watch` queue inspection. Synchronize storage, Telegram, operations and product guides; preserve historical audit/evidence records.
4. Update existing tests and cover cross-recipient/direct-send suppression, exact expiry, missing-delay fallback, cancellation, outcome-write failure, restart reset, durable notification deadlines and fresh schema shape.

## Verification

- [x] Shared cooldown suppresses notifications and replies without consuming queued attempts and expires at the exact deadline.
- [x] Positive Telegram delay is honored; absent/nonpositive delay uses 30 seconds.
- [x] Cancellation and outcome-write failure do not lose an observed limit in the current worker.
- [x] Restart clears the shared cooldown while preserving notification retry deadlines, attempts and ordering.
- [x] Fresh schema omits delivery_gates; no additional migration or global repository remains.
- [x] Go 1.27.3 tests, race tests, vet and application build pass after selecting the repository Node version.
- [x] Documentation links/formatting pass; historical ADR contents remain intact apart from lifecycle metadata and reciprocal references.

## References

- [Prior delivery decision](0009_choose-notification-retry-policy.md)
- [Invalid-token policy](0012_fail-fast-on-invalid-telegram-token.md)
- [SQLite persistence](0004_choose-sqlite-persistence.md)
- [Service transaction ownership](0011_move-transaction-boundaries-to-services.md)
- [Worker implementation](../../internal/services/notifications/worker.go)
- [Storage guide](../storage.md)

## More Information

### 2026-11-03: Implementation verification

Implemented the worker-owned cooldown and removed the global repository, state model, Store fields and initial-schema table. Offline fixtures verify shared admission, exact expiry, fallback delay, cancellation, failed outcome persistence, restart reset, durable notification retry state, recipient recovery and fresh schema shape. Full Go 1.27.3 tests, race tests, vet and application build passed with Node v26.13.0 selected through nvm. Markdown links/fences, reciprocal supersession references, ADR index and git diff --check passed. ADR 0009's historical contents were preserved apart from lifecycle metadata and the replacement link. See [verification evidence](../implementation-progress.md). No live Telegram/portal or deployment acceptance was performed.
