---
status: accepted
date: 2026-11-05
decision-makers: Repository maintainer
---

# Defer credential encryption during WIP

## Context and Problem Statement

Case Watch is an undeployed work-in-progress application with no existing databases. ADR 0006 introduced field encryption, a separate keyring, provisioning, startup authentication and rotation before the basic product architecture had stabilized. The maintainer wants the smallest working application first and will reconsider encryption when a concrete deployment or threat-model requirement exists.

The portal still requires the original case number and password for unattended checks, so hashing cannot replace reversible storage.

## Decision

Store each enrollment's case number and portal password as plaintext columns in the protected SQLite database. Remove application-level encryption, keyring configuration, provisioning, rotation and ciphertext verification. Revise the consolidated initial schema directly; do not add a compatibility migration because there are no existing databases.

Retain opaque enrollment identities for removal fencing, mode-0600 database files, restricted database directories, owner-scoped access, sanitized diagnostics, separate Telegram credential messages, memory-only drafts and best-effort credential-message deletion. Credentials must not be copied into history, notification payloads, logs, errors or replies.

This decision supersedes ADR 0006 and overrides encryption/keyring clauses in ADRs 0008 and 0011. It does not select a future encryption design or claim that plaintext storage is suitable for production.

## Consequences

- Good, because startup, configuration, deployment, persistence and maintenance contain less code while the application is being shaped.
- Good, because development requires only the Telegram token, database path and authorization configuration.
- Bad, because anyone who obtains the SQLite database or a copy can read portal credentials.
- Bad, because encryption must be designed and implemented before deployment if the later threat model requires protection against database disclosure.
- Acceptable for now, because the application is undeployed, has no existing databases and the maintainer explicitly accepts this WIP tradeoff.

## Implementation Plan

- **Affected paths**: `internal/credentials/`, `internal/application/contracts.go`, `internal/data/`, `internal/tracking/`, `internal/service/`, `internal/config/`, `cmd/case-watch/`, `compose.yaml`, `.env.example`, `README.md`, and maintained documents under `docs/`.
- **Dependencies**: remove no third-party module; standard-library cryptography was isolated to the deleted credential package.
- **Patterns to follow**: store `application.Credentials` with the enrollment; keep enrollment creation and removal transactional; keep credentials out of diagnostics and notification data.
- **Patterns to avoid**: no compatibility migration, encryption feature flag, dormant keyring abstraction, plaintext logging or automatic database reset.

### Verification

- [x] The initial schema contains `case_number` and `password` and no encryption metadata columns.
- [x] Enrollment, restart checks and removal use plaintext credentials while retaining identity fencing.
- [x] Keyring code, key configuration, provisioning and rotation commands are absent.
- [x] Telegram draft/deletion and diagnostic-sanitization behavior remains covered by tests.
- [x] Full Go tests, race tests, vet and application build pass with Go 1.27.3.
- [x] Maintained documentation contains no active keyring or encryption instructions.

## Alternatives Considered

- Keep ADR 0006 encryption: protects database-only disclosure but retains complexity the maintainer does not currently need.
- Store credentials outside SQLite: complicates ownership, atomic enrollment and unattended scheduled checks without eliminating secret storage.
- Hash passwords: rejected because the portal requires the original password.

## More Information

Revisit encryption before deployment, before storing real credentials outside the maintainer's accepted development environment, or when a concrete database-disclosure threat model is adopted.
