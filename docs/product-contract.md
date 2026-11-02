# Case Watch product contract

Confirmed scope, 2026-11-01. This document makes the accepted ADRs concrete for implementation; it does not claim a running service. All bot text is English. Preserve portal stage text verbatim, including its original language. Never echo credentials or include them in logs/errors. Operate one instance with private chats and numeric sender identity; authorize before parsing enrollment input or accessing owner data. Allow-all does not bypass ownership.

## Commands and conversations

| Command | Behavior |
| --- | --- |
| `/start`, `/help` | Explain tracking, commands, privacy limitations and the six Warsaw weekday checks. Never initiate a portal request. |
| `/enroll` | Start/restart a guided conversation for an owner with no tracked case. Refuse replacement if a case already exists; explain `/remove` first. |
| `/cancel` | Clear any enrollment draft or removal confirmation, including plaintext references. No portal request. |
| `/status` | Return only the owner's case using ADR 0007 freshness, cooldown and in-flight sharing rules. Explain when no case exists. |
| `/remove` | Request explicit confirmation before deleting the owner's tracked case. |

Commands accept no credential arguments. Reject argument-bearing enrollment commands without echoing their input and attempt credential-message deletion. Collect case number and password in separate ordinary private text messages, never one combined message. A draft exists only in process memory and expires after 10 minutes of inactivity (at the deadline it is expired); each accepted draft input refreshes inactivity. `/enroll` restarts with a cleared draft, `/cancel` clears it, and process restart discards all drafts. No plaintext draft reaches SQLite. Validation failures receive generic English guidance without echoing input. Attempt deletion of every credential-bearing message, including invalid number/password input and rejected credential arguments, on a best-effort basis. A deletion failure must not leak message content or pretend Telegram copies are gone. Enrollment over Telegram is not end-to-end encrypted; platform copies may remain.

After receiving the password, perform one bounded logical portal check with cancellation, existing request limits and no automatic retry. On success, atomically commit encrypted number/password and the initial stage/time baseline; enforce one case per owner transactionally. Only after commit send a separate tracking confirmation. The baseline creates no change notification. On validation, portal, encryption, storage or cancellation failure, leave enrollment uncommitted and clear the password; ask the user to restart rather than silently retry. If confirmation delivery fails after commit, tracking remains enrolled; `/status` can reveal it. Do not undo the committed enrollment or invent a durable command guarantee.

## Removal

`/remove` clears any enrollment draft and starts an in-memory confirmation valid for 10 minutes from the prompt (not extended by unrelated messages). Require the exact separate text `REMOVE` from the same authorized private sender before the deadline. Explain that credentials, history and undelivered notifications will be deleted and cannot be reconstructed. `/cancel`, a fresh `/enroll`, or restart clears confirmation; expiration requires a new `/remove`. Other text never confirms removal. A user without a case receives an explanatory reply.

Confirmed removal stops new work, cancels/drains existing checks and notification sends for that enrollment, and transactionally deletes its encrypted credentials, baseline/cache, history and pending notifications. Use an immutable enrollment identity or generation fence: late portal results and send acknowledgements must not recreate data or affect a later enrollment. Success is reported only after coordinated removal completes. A Telegram send already accepted externally cannot be recalled; explain this limitation without retaining deleted pending work. A failure must be reported without a false success claim. Retain the owner's persisted manual cooldown and recipient delivery gates (blocked/permanent/exhausted pauses), and retain global token/rate-limit gates, so re-enrollment cannot bypass limits. The enrollment row is soft-deleted with `deleted_at`; only its ID, owner and deletion timestamp remain, and normal operations exclude it. Deletion concerns application data; it does not promise forensic erasure of SQLite pages or Telegram messages.

This explicit owner-requested deletion is the scoped exception to ADRs 0004/0009 retained history/notification requirements. Authorization withdrawal, send failure, retry exhaustion, restart and ordinary cancellation never discard pending transitions. Removing access continues to suppress and retain data under ADR 0005; it is distinct from `/remove`.

## Checks and notifications

Follow ADR 0007: 60-second persisted user manual and case attempt-start guards; successful-cache freshness measured from completion; joins/cached replies do not consume attempts; failed refreshes preserve the old stage/time. Manual and scheduled observations share the atomic stage/history/queue transaction. Direct replies never acknowledge durable changes. Preserve A → B → A as two transitions. Scheduled runs are sequential at 08:00, 10:00, 12:00, 14:00, 16:00 and 18:00 Monday–Friday in Europe/Warsaw, holidays included; skip missed slots and overlapping runs.

Follow ADR 0009: persistent recipient ordering, one send in flight per recipient, initial attempt plus five retries after 30/60/120/240/480 seconds, retained paused head on exhaustion, authorization suppression, blocked/unblocked recovery, and persisted global rate-limit/token gates. Never hold a database transaction over network I/O. Diagnostic output contains no tokens, credentials, message bodies or raw portal/Telegram errors.

## Acceptance and boundaries

Offline synthetic tests must demonstrate draft expiry/clearing and deletion attempts on invalid input; transactional duplicate-enrollment rejection and baseline rollback; removal confirmation expiry, cancellation/draining and late-result fencing; retained cooldown/gates after removal/re-enrollment; and distinction between explicit removal and suppressed/failed delivery. Later milestones add behavior tests with fake time and endpoints. No live portal or Telegram requests, real keys, deployment, backup tooling or target-device claims are authorized by this contract implementation.
