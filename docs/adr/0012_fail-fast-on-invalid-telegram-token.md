---
status: accepted
date: 2026-11-03
decision-makers: Repository maintainer
---

# Fail fast on an invalid Telegram token

## Context and Decision

ADR 0009 suspends outbound delivery globally after token rejection. The persisted suspension requires a separate operator resume command even after correcting the environment. The maintainer confirmed that stopping portal checks during this failure is acceptable and approved this replacement policy on 2026-11-03.

This record supersedes only the token-failure policy and token gates in [ADR 0009](0009_choose-notification-retry-policy.md). Its ordering, retention, recipient recovery, retry and rate-limit policies remain accepted.

After local configuration, storage and credential validation, validate the token through a context-cancellable Telegram getMe request bounded to 15 seconds before recovery, polling, delivery or scheduling starts. Any validation failure prevents startup; network failures are not diagnosed as invalid credentials. Emit only fixed sanitized diagnostic categories.

A structured unauthorized response during polling, sending or credential-message deletion stops the entire service through normal cancellation and draining, returning an operational failure. Do not infer invalid credentials from arbitrary error text. Other failure categories retain their existing handling.

Explicit token rejection cannot represent message acceptance. Undo that notification reservation's cycle and lifetime increments without marking it delivered, advancing the queue, changing recipient pauses or consuming a retry. Finish this persistence under a bounded cancellation-independent context. Storage failure remains fatal; uncertain crash recovery retains its existing conservative behavior. Serialize outbound sends and stop further send admission once token rejection is observed.

Remove token_paused from application state and the initial SQLite schema, and remove --resume-token. The application is not deployed, so revise the initial schema directly without an upgrade path. Operators correct CASE_WATCH_TELEGRAM_BOT_TOKEN and restart normally. A successful startup check cannot prevent later revocation, hence runtime shutdown is also required.

## Alternatives and Consequences

- Persisted sending suspension permits portal monitoring to continue, but adds durable configuration-related state and a manual recovery step. Rejected because this continuity is unnecessary.
- Logging and continuing sends wastes requests and recipient retry budgets. Rejected.
- Startup validation alone cannot handle runtime revocation. Rejected.
- Fail-fast startup and runtime shutdown simplify recovery, at the cost of stopping monitoring and requiring Telegram connectivity during startup. Selected.

No token reload, automatic credential repair, notification deletion, new dependency or live acceptance test is introduced. Supervisor restart policy remains deployment configuration.

## Implementation Plan

- internal/telegram: explicit Validate(ctx) using getMe, structured runtime unauthorized diagnostics for polling/deletion, existing send classification.
- internal/service: require transport validation before workers; turn runtime invalid-token diagnostics/outcomes into fatal cancellation with normal drain.
- internal/notifications: fail closed after token rejection and restore the rejected reservation's counters; preserve all other delivery policies.
- internal/data: remove token state/repository API and token_paused from the initial schema.
- cmd/case-watch: remove resume handler, dispatch, usage and token queue metadata.
- Update operations, configuration, Telegram and storage guides. Append dated evidence without rewriting historical records.

## Verification

- [x] Offline adapter tests cover bounded validation success/failure and sanitized runtime rejection in polling and deletion.
- [x] Service tests prove validation precedes workers and runtime rejection causes failure and drain.
- [x] Worker tests prove rejected sends preserve retry budgets, gate subsequent sends and resume with a new worker.
- [x] Fresh databases initialize with the token suspension column absent; existing storage and delivery tests pass.
- [x] Removed command is rejected. Existing rate-limit, uncertain recovery and recipient behavior tests pass.
- [x] Go 1.27.3: go test ./..., go test -race ./..., go vet ./..., build ./cmd/case-watch; Markdown links and git diff --check pass.
- [x] Report offline verification separately from live Telegram/portal and deployment acceptance.

## More Information

### 2026-11-03: Implementation verification

Implemented startup validation, runtime shutdown, rejected-attempt counter restoration, removal of token maintenance/state from the initial schema. Full tests, race tests, vet and application build passed with Go 1.27.3 and Node v26.13.0 selected through nvm. Offline fixtures cover startup rejection, polling/deletion rejection, runtime drain, counter restoration at the final retry, refusal of later sends even after persistence failure, ordinary restart and command removal. See [verification evidence](../implementation-progress.md). No live Telegram/portal or deployment acceptance was performed.
