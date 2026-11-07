---
status: accepted
date: 2026-11-01
decision-makers: Repository maintainer
---

# Choose startup allowlist authorization and private case enrollment

## Context and Problem Statement

Case Watch initially serves the maintainer and his wife. Before adding Telegram commands and case enrollment, it needs a consistent way to determine who may use the service and whose cases they may access.

[ADR 0003](0003_choose-telegram-bot-integration.md) selects Telegram long polling, while [ADR 0004](0004_choose-sqlite-persistence.md) defines persistent case state and recipient associations. Neither establishes authorization or enrollment. The maintainer proposed startup configuration containing allowed Telegram user IDs and an explicit allow-all flag and confirmed the model below.

## Decision Drivers

- Keep configuration simple for a private, single-instance service.
- Default to restricted access and reject invalid configuration explicitly.
- Apply the same authorization policy to commands, enrollment, and background work.
- Keep case ownership separate from eligibility to use the bot.

## Considered Options

- Startup environment variables containing an allowlist and explicit allow-all flag.
- A YAML or JSON configuration file.
- Database-managed access and administrative bot commands.

## Decision Outcome

Use environment variables as the initial source of truth for access eligibility:

```dotenv
CASE_WATCH_ALLOW_ALL_USERS=false
CASE_WATCH_ALLOWED_USER_IDS=123456789,987654321
```

The IDs above are synthetic examples. Parse the allowlist as comma-separated positive decimal Telegram user IDs represented by Go `int64`; trim whitespace and treat duplicate IDs as one entry. Missing `CASE_WATCH_ALLOW_ALL_USERS` defaults to `false`. When present, accept only `true` or `false` after trimming whitespace. Reject malformed values and invalid IDs before starting any polling or background work. A wholly empty allowlist is permitted only in allow-all mode; malformed nonempty lists are errors even in that mode.

| Configuration | Behavior |
| --- | --- |
| Allow-all is false | Serve only senders whose numeric ID is in the allowlist |
| Allow-all is true | Allow any eligible private-chat user; the allowlist does not restrict eligibility |
| Restricted mode with an empty allowlist | Refuse startup with a clear configuration error |
| Malformed boolean or user IDs | Refuse startup |
| Configuration changed | Apply on restart; no live reload initially |

### Identity, ownership, and enrollment

- Support ordinary private chats only, including in allow-all mode. Reject group/channel interactions and updates without a usable sender identity before enrollment or case operations.
- Derive identity from Telegram's numeric sender ID, not a username, chat identifier supplied by the user, or enrollment text. User IDs require `int64` storage.
- Check authorization before processing command arguments, enrollment input, or case operations. Apply it to callback interactions if those are introduced.
- An authorized user enrolls their own case through a guided private conversation. Bind ownership automatically to the sender identity; never request or accept an owner ID from enrollment input.
- Users may view, modify, or remove only their own case associations and receive only notifications authorized for those associations. Enforce ownership in application/storage operations as well as handler routing.
- Allow-all changes eligibility, not ownership or administrative privileges. It does not expose existing cases to other users. No admin role or administrative access-management commands are introduced.

### Access removal and background work

Evaluate background checks and delivery against the current startup policy. After a restart removes a user from restricted-mode access, stop notifications and portal checks performed solely for that user. Recheck recipient eligibility immediately before delivery, including pending notifications restored from storage. Retain suppressed pending records and stored case data initially rather than marking unsent work delivered or deleting it.

If another authorized association still requires a case check, it may continue, but no result is delivered to an unauthorized recipient. Retained pending work becomes eligible again if access is restored; ordinary notification failure policies remain separate.

### Consequences

- Access is inspectable and works equally with direct execution, service managers, and containers; Docker is not required.
- Access changes require a restart. Removing a user does not erase their data.
- Eligibility and ownership must remain explicit checks across interactive and background paths; handler-only filtering is insufficient.
- Allow-all permits enrollment beyond the initial two users but does not establish resource quotas or readiness for public operation.
- Environment parsing adds no new dependency. A future file-based or dynamically managed policy can be considered if operational needs change.

## Scope and Non-goals

This decision selects authorization configuration, identity, ownership, and the enrollment entry model. Credential encryption, key management, exact enrollment prompts and commands, interruption/recovery of enrollment conversations, notification retries, and user-data deletion remain separate decisions. It does not make incoming Telegram commands durably processed or supersede existing ADRs. No application code or runtime dependency is added by this record.

## Implementation Plan

These tasks remain pending:

- [x] Add startup parsing and validation under `internal/config/` with the defaults and error rules above; document the two variables in an example environment file containing synthetic IDs only.
- [x] Implement a shared eligibility policy under `internal/auth/`, accepting ordinary `int64` IDs rather than Telegram library types. Reference this ADR from its entry point.
- [x] Apply private-chat, sender, and eligibility checks in `internal/telegram/` before dispatching enrollment or case operations. Bind enrollment ownership to the authenticated sender.
- [x] Carry owner identity into case-association operations under `internal/storage/` and enforce ownership when reading or mutating associations. Do not infer ownership from a caller-supplied case identifier alone.
- [x] Gate background scheduling and pending-notification delivery on the same policy. Retain suppressed data without treating it as delivered.
- [x] Define credential protection and detailed enrollment behavior before implementing collection or persistence of real portal passwords.
- [x] Add offline tests with synthetic Telegram updates, IDs, and case associations; no real Telegram or portal access is needed.

### Verification

- [x] Missing allow-all defaults to restricted mode; empty restricted lists and malformed values fail startup before network work.
- [x] Whitespace and duplicate valid IDs parse consistently; nonpositive, overflowing, empty interior, and nonnumeric entries are rejected.
- [x] Restricted mode allows listed IDs and rejects others; allow-all permits otherwise eligible private users without bypassing ownership.
- [x] Group/channel updates and missing sender identities cannot reach enrollment or case operations.
- [x] Enrollment binds the sender as owner; knowing another user's case identifier does not grant access to their association.
- [x] Removing a user and restarting suppresses new and pending deliveries and checks solely for that user while preserving data.
- [ ] Restoring access makes retained pending work eligible again; checks needed by another authorized association continue without delivering to a removed user.
- [x] Policy/configuration code uses no Telegram types; offline tests exercise both interactive and background authorization paths.

## Pros and Cons of the Options

### Startup environment variables

Simple for two values, supported by common runtime environments, and requires no parser dependency or administrative API. Chosen because it satisfies the initial access model. Long allowlists and frequent updates would become cumbersome, and restart is required.

### YAML or JSON file

Provides structured configuration and can scale to more settings. Not chosen initially because these two settings do not require a configuration file, format selection, or file/environment precedence rules. It remains a reasonable future option.

### Database-managed access and admin commands

Enables changes without restarting and richer administration. Not chosen because it introduces admin identity, bootstrap, and policy mutation flows unnecessary for the initial service.

## More Information

- [Telegram User identity and numeric ID requirements](https://core.telegram.org/bots/api#user).
- [Telegram Chat types](https://core.telegram.org/bots/api#chat).

The maintainer confirmed this authorization and enrollment model on 2026-11-01. Revisit startup-only configuration if frequent access changes become necessary, and revisit chat restrictions explicitly before supporting groups or channels. Implementation and live validation remain pending.

### 2026-11-01: Confirmed single-case command and removal scope

The maintainer's implementation plan defines [the product contract](../product-contract.md): one tracked case per user, replacement refused, separate-message `/enroll`, ephemeral 10-minute inactivity drafts and explicit 10-minute `/remove` confirmation. `/start`, `/help`, `/cancel` and `/status` complete the command surface. Confirmed removal deletes that owner's credentials, history and pending work with cancellation/draining and late-result fencing; it retains user cooldowns and recipient/global gates. This is explicit data deletion, distinct from authorization withdrawal, which continues to suppress and retain data as specified above. See `internal/application/contracts.go`; later implementation must test ownership, expiry, cancellation and removal races offline.

### 2026-11-01: Independent implementation evidence

`internal/config`, `internal/auth`, Telegram authentication, owner-scoped storage and shared tracking/scheduler/worker policy implement the current one-case-per-owner contract. Tests cover malformed defaults/IDs, identity and ownership isolation, authorization before access, and suppression without attempt consumption or deletion. Restart loads the new immutable startup policy; restored eligibility permits retained work subject to persisted gates. The composite shared-association verification item stays unchecked: this implementation has no multiple-owner shared-case association model, so it cannot demonstrate that future behavior. See the single-case product-contract amendment and audit matrix.

Checklist evidence maps to the reviewed implementation and offline tests in [the independent audit](../implementation-audit.md) and exact command results in [implementation progress](../implementation-progress.md). Unchecked composite items retain their pending, conditional or explicitly deferred clauses; acceptance of this ADR is not a claim of live, Docker or Pi validation.

### 2026-11-07: User authorization removed

[ADR 0017](0017_remove-user-authorization.md) replaces this record's startup
allowlist, shared eligibility policy and authorization suppression requirements.
All ordinary private-chat users may now enroll and manage their own case.
Private sender validation, owner isolation and enrollment/removal behavior
remain in effect. Earlier implementation evidence above is historical.
