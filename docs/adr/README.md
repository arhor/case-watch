# Architecture Decision Records (ADR)

An Architecture Decision Record (ADR) captures an important architecture decision along with its context and consequences.

## Conventions

- Directory: `docs/adr`
- Naming:
  - Use numbered files in `docs/adr/`: `0001_choose-database.md`
  - Increment the highest existing index; pad to four digits and do not reuse gaps
- Status values: `proposed`, `accepted`, `rejected`, `deprecated`, `superseded`

## Workflow

- Create a new ADR as `proposed`.
- Discuss and iterate.
- When the team commits: mark it `accepted` (or `rejected`).
- If replaced later: create a new ADR and mark the old one `superseded` with a link.

## ADRs

- [Adopt architecture decision records](0001_adopt-architecture-decision-records.md) (accepted, 2026-10-30)
- [Choose Go for the Case Watch service](0002_choose-go.md) (accepted, 2026-10-31)
- [Choose go-telegram/bot with long polling](0003_choose-telegram-bot-integration.md) (accepted, 2026-10-31)
- [Choose SQLite with modernc for case state and pending notifications](0004_choose-sqlite-persistence.md) (accepted, 2026-10-31)
- [Choose startup allowlist authorization and private case enrollment](0005_choose-user-authorization-and-enrollment.md) (accepted, 2026-11-01)
- [Encrypt portal credentials with a separately stored key](0006_encrypt-portal-credentials.md) (superseded by ADR 0014, 2026-11-05)
- [Choose weekday polling and rate-limited fresh status checks](0007_choose-polling-and-status-behavior.md) (accepted, 2026-11-01)
- [Choose Docker Compose with persistent storage and defer backups](0008_choose-container-deployment-without-backups.md) (accepted, 2026-11-01)
- [Choose bounded retries and ordered notification recovery](0009_choose-notification-retry-policy.md) (superseded, 2026-11-01)
- [Adopt golang-migrate for database migrations](0010_adopt-golang-migrate.md) (proposed, 2026-11-02)
- [Move transaction boundaries to services](0011_move-transaction-boundaries-to-services.md) (accepted, 2026-11-02)
- [Fail fast on an invalid Telegram token](0012_fail-fast-on-invalid-telegram-token.md) (accepted, 2026-11-03)
- [Keep the Telegram send cooldown in memory](0013_keep-telegram-send-cooldown-in-memory.md) (accepted, 2026-11-03)
- [Defer credential encryption during WIP](0014_defer-credential-encryption-during-wip.md) (accepted, 2026-11-05)
- [Adopt a three-layer application structure](0015_adopt-three-layer-application-structure.md) (accepted, 2026-11-05)
