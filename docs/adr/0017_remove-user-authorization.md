---
status: accepted
date: 2026-11-07
decision-makers: Repository maintainer
---

# Remove user authorization

## Context

The maintainer requested removal of auth functionality. ADR 0005 introduced a
startup allowlist shared by Telegram intake, tracking and delivery. It required
access configuration even though Telegram already supplies the private sender
identity used to scope each owner's case.

## Decision

Remove the user eligibility policy, `internal/auth`, `domain.Policy`, and the
`CASE_WATCH_ALLOW_ALL_USERS` and `CASE_WATCH_ALLOWED_USER_IDS` configuration.
All ordinary private-chat users may enroll and manage their own case. Old access
configuration values are ignored; operators can remove them without a database
migration. Scheduled checks and notification delivery include all enrolled
owners, subject to existing cooldowns, removal fencing and delivery gates.

Keep Telegram transport identity validation in its entry point: accept only
non-bot senders with positive matching sender/chat IDs in an ordinary private
chat. Keep owner-scoped storage, one active case per owner, enrollment identity
fencing and explicit removal confirmation. Portal authentication and Telegram
bot-token validation remain required for their external integrations.

This replaces the startup eligibility and authorization suppression portions of
ADR 0005 and dependent authorization suppression requirements in ADRs 0007,
0009 and 0013. Their enrollment, ownership, scheduling, retry, blocked-recipient
and data-retention decisions continue to apply.

## Consequences

Every private-chat user can now cause portal checks and receive notifications
for their own enrollment. Existing enrollments and pending work previously
excluded by an allowlist become eligible after upgrading, subject to existing
gates. There is no restricted access mode or administrative user list. No stored
data is deleted or transformed.

## Verification

Offline tests cover startup with only the token and database path, private-chat
sender validation for messages and membership events, enrollment and ownership
isolation, scheduled checks and delivery for a new owner without access
configuration. Existing removal, cooldown, retry, cancellation and lifecycle
coverage remains in place. See the dated evidence in
[implementation progress](../implementation-progress.md).
