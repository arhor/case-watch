---
status: accepted
date: 2026-11-01
decision-makers: Repository maintainer
---

# Choose bounded retries and ordered notification recovery

## Context and Problem Statement

Case Watch can observe a case transition while Telegram delivery is unavailable. [ADR 0004](0004_choose-sqlite-persistence.md) requires durable pending notifications and accepts duplicates when send acceptance is uncertain. [ADR 0003](0003_choose-telegram-bot-integration.md) selects the Telegram library but leaves application delivery policy open. [ADR 0005](0005_choose-user-authorization-and-enrollment.md) requires authorization checks before delivery.

The maintainer selected exponential backoff starting at 30 seconds, five retries, and a factor of 2.0. Retained notifications must resume automatically after unblocking and each transition must be delivered in order. After the final retry fails, retain the notification and pause that recipient's queue rather than discard work or deliver later transitions out of order.

## Decision Drivers

- Bound repeated send attempts during temporary outages.
- Respect Telegram rate limits and blocked-recipient state.
- Preserve observed transition order and durable work across restarts.
- Distinguish delivery failure, queue pause, and confirmed delivery.
- Retain the existing acceptance of possible duplicate messages.

## Considered Options

- Bounded exponential retries with a retained, paused queue after exhaustion.
- Unlimited periodic retries.
- Drop failed notifications after a retry limit and continue later messages.
- Combine accumulated transitions into one catch-up notification.

## Decision Outcome

### Retry schedule

Each notification has one initial send attempt and at most five retries in a delivery cycle: six attempts total. After a retryable failure, wait the corresponding interval from the failed attempt's completion:

| Retry | Delay |
| --- | --- |
| 1 | 30 seconds |
| 2 | 60 seconds |
| 3 | 120 seconds |
| 4 | 240 seconds |
| 5 | 480 seconds |

Use the fixed factor-2 schedule without jitter initially. Failed initial/retry attempts, including uncertain network timeouts and rate-limit responses, consume the cycle budget. Waiting, authorization suppression, and known blocked-state pauses do not create attempts or consume retries. Reserve actual send attempts durably before network access so crashes cannot reset the budget.

### Failure classification

| Outcome | Behavior |
| --- | --- |
| Telegram accepts the message | Persist confirmation, including returned message ID, then allow the next queued transition |
| Network/timeout or Telegram server failure | Retain the notification and schedule the next retry if available |
| Telegram rate limit | Respect `retry_after`; the next attempt must wait at least the greater of the policy delay and Telegram's requested delay |
| Recipient blocked the bot | Pause that recipient, retaining the unsent queue; do not repeatedly probe by sending |
| Invalid/revoked bot token | Suspend outbound delivery globally and report a sanitized operational error; correction is required rather than spending every recipient's retry budget |
| Other permanent send failure, such as an invalid destination or payload | Retain the failed notification, pause that recipient's queue, and report an operational error instead of retrying unchanged input |
| Authorization removed | Suppress delivery under ADR 0005 without deleting data or consuming retry attempts |

Use structured Telegram error information wherever available; do not treat every forbidden response as definitive proof of blocking. Retain a sanitized failure classification and do not expose tokens, credentials, or message bodies through diagnostics.

Persist the Telegram rate-limit deadline and gate outbound sends until it expires. Initially apply a reported send rate limit globally, conservatively avoiding attempts to bypass it through other recipients. The library's polling backoff is independent of this outbound delivery policy.

### Ordered delivery and exhaustion

Maintain a persistent enqueue sequence for each recipient, covering transitions across their tracked cases. Permit at most one in-flight notification per recipient. Send the oldest outstanding notification first; later transitions do not overtake an earlier retrying, blocked, failed, or exhausted record. Different recipients need not share ordering, except for a global sending suspension or rate-limit gate.

After all six attempts fail, mark the notification as exhausted, retain it, and pause that recipient's queue with a clear operational error. New observations may continue appending history and notifications, but delivery remains paused. Do not automatically restart exhausted cycles on process restart or on the next case observation. Provide an explicit operator recovery action to resume after correcting the cause; this is maintenance functionality, not a new Telegram admin role.

An unsuccessful send must never be marked delivered to advance the queue. Do not expire, combine, or discard retained transitions under this policy. A sequence A → B → A produces two ordered change notifications as required by ADR 0004.

### Blocking and automatic resume

Subscribe to Telegram `my_chat_member` updates in addition to the update types required by commands. For private chats, use the bot's transition to blocked/unblocked membership status to pause/resume that recipient. Apply the private-chat and identity checks from ADR 0005 to these updates; do not confuse the bot's membership update with permission to access other users' data.

On a valid unblock update, automatically make retained notifications eligible again with a fresh retry cycle for the oldest unsent item. Preserve lifetime attempt history separately from the reset cycle budget. Current authorization and any global token/rate-limit gate still apply. There is no requirement to send `/start` before normal unblock-event recovery.

An authenticated incoming private message may also establish that delivery can resume if the unblock event was missed. This is a fallback, not a requirement for the ordinary automatic path. Telegram update retention is finite, and ADR 0003 does not guarantee durable incoming-update processing: automatic recovery depends on observing an unblock or subsequent private interaction. Do not claim guaranteed detection after arbitrary downtime. Exhaustion/permanent-error pauses without evidence of a block/unblock cycle require explicit operator recovery rather than being cleared by unrelated messages.

### Persistence and restart behavior

Store delivery status, recipient enqueue order, cycle/lifetime attempts, last failure category, next-attempt deadline, confirmed Telegram message ID when available, recipient pause reason, and any global rate-limit deadline in SQLite. Persist these independently of successful portal observations. Use short transactions; never hold a database transaction across a Telegram call.

After restart, reload pending work and gates. A due notification may be attempted without replaying all its missed retry intervals. In-flight attempts without a recorded outcome are uncertain failures: preserve their reserved attempt count and apply the corresponding delay, or exhaust the cycle if no retries remain. Retrying an accepted-but-unrecorded message may duplicate it; this is the explicitly accepted ADR 0004 tradeoff.

### Consequences

- Temporary failure recovery is automatic within a bounded cycle; long outages may require operator recovery after exhaustion.
- Strict ordering can hold all notifications for one recipient behind a single failed item. This is intentional and must be visible operationally.
- Unblocking normally resumes the backlog without user commands, subject to event availability and authorization.
- Retained queues and failure state need persistent storage and maintenance controls.
- Neither a successful API call nor this retry policy proves the human read a message. Exactly-once delivery is not guaranteed.

## Scope and Non-goals

This policy governs durable case-change notifications. It does not retry government-portal requests, change ADR 0007's cooldowns, define automatic retries for direct command replies, or provide durable Telegram command processing. It adds no runtime dependency or live network work. Existing ADRs remain accepted.

## Implementation Plan

These tasks remain pending:

- [x] Extend `internal/storage/` with versioned delivery-state migrations, ordered recipient queues, atomic attempt reservation, pause/resume operations, and persisted retry/gate deadlines.
- [x] Implement delivery coordination under `internal/notifications/` with an injected clock and sender interface using ordinary Go types. Reference this ADR at its entry point.
- [x] Map Telegram send results into the policy's failure categories within `internal/telegram/`; include private block/unblock update handling and sanitized operational diagnostics.
- [x] Enforce current authorization and persisted recipient/global gates before each actual send. Add explicit maintenance recovery for exhausted/permanent-failure queues without silently marking messages delivered.
- [x] Add offline fault-injection tests with temporary databases, fake time, and synthetic responses; document diagnosis and recovery without live Telegram or portal tests.

### Verification

- [x] One initial attempt plus five retries uses delays 30, 60, 120, 240, and 480 seconds; failed final attempts retain work and pause the recipient.
- [x] `retry_after` never shortens backoff; persisted global rate-limit gates delay sends to all recipients until eligible.
- [x] Transient errors, uncertain outcomes, blocking, bad tokens, permanent errors, and authorization suppression produce distinct states and no secret-bearing diagnostics.
- [x] Each recipient receives every transition in enqueue order; a failed head prevents overtaking while unrelated recipient queues can progress.
- [x] Unblock events automatically resume retained work with a fresh cycle, preserving lifetime counts and honoring authorization and global gates.
- [x] Restart preserves attempts, order, pauses, and deadlines. Crash boundaries before send and after acceptance/before persistence retain the correct uncertainty and possible-duplicate behavior.
- [x] Exhaustion does not reset on new observations or restart; explicit recovery is required outside block/unblock recovery.
- [x] Tests use fake time and synthetic data without live Telegram or government-portal requests.

## Pros and Cons of the Options

### Bounded retries with retained paused queues

Chosen because it bounds temporary-failure traffic while preserving transitions and exposing exhaustion explicitly. Requires recovery controls and can delay a recipient's entire backlog.

### Unlimited periodic retries

Could recover automatically after arbitrarily long temporary outages without operator intervention. Not chosen because the maintainer selected five retries rather than indefinite attempts.

### Drop exhausted notifications and continue

Keeps later messages moving and limits queue growth. Rejected because it silently loses observed transitions and conflicts with the selected ordered-retention requirement.

### Combine accumulated changes

Reduces catch-up messages. Not chosen because the maintainer wants each transition delivered individually in order.

## More Information

Sources reviewed on 2026-11-01:

- [Telegram rate-limit response parameters](https://core.telegram.org/bots/api#responseparameters).
- [Telegram update types, block/unblock events, and update retention](https://core.telegram.org/bots/api#getting-updates).

The maintainer confirmed the retry schedule, automatic unblock recovery, ordered delivery, and retained queue pause after exhaustion. Revisit if operator intervention during extended outages becomes undesirable. Implementation and live delivery validation remain pending.

### 2026-11-01: Explicit owner-requested removal exception

The confirmed [product contract](../product-contract.md) permits deletion of an owner's case credentials, baseline/history and pending notifications only after an explicit, unexpired `/remove` confirmation. Cancel/drain that enrollment's checks and sends and fence late results so deletion cannot resurrect data or affect re-enrollment. Preserve user cooldowns and recipient/global delivery gates. This scoped exception is deliberate user-requested deletion, not silent notification loss: authorization suppression, unsuccessful sends, retry exhaustion and restart must still retain undelivered transitions under the policy above. Later storage/coordinator tests must verify atomic removal, late-result rejection and gate retention with synthetic data.

### 2026-11-01: Independent implementation evidence

Storage/worker/Telegram/service implement durable oldest-first reservation, six-attempt backoff, persistent recipient/global gates, uncertain recovery, explicit maintenance and confirmed owner removal. The independent audit found and corrected three interleavings/evidence gaps: direct sends now share serialized gate/send/outcome admission with durable sends; known direct rate/token outcomes persist through bounded cancellation-independent cleanup; explicit unblock evidence resumes even when the preceding block update was missed, while unrelated interaction cannot clear permanent/exhausted pauses. Fresh independent race tests of notifications/Telegram/storage/service passed after reviewing the fixes. See `docs/implementation-audit.md` for requirement mapping and regression evidence. These checks are offline synthetic policy evidence, not live Telegram delivery or Docker validation.

Checklist evidence maps to the reviewed implementation and offline tests in [the independent audit](../implementation-audit.md) and exact command results in [implementation progress](../implementation-progress.md). Unchecked composite items retain their pending, conditional or explicitly deferred clauses; acceptance of this ADR is not a claim of live, Docker or Pi validation.

### 2026-11-03: Token policy superseded

[ADR 0012](0012_fail-fast-on-invalid-telegram-token.md) replaces this record’s token-failure policy and token suspension gates with startup validation and whole-service shutdown on runtime token rejection. All other delivery policies remain accepted.
