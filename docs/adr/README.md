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
- [Encrypt portal credentials with a separately stored key](0006_encrypt-portal-credentials.md) (accepted, 2026-11-01)
- [Choose weekday polling and rate-limited fresh status checks](0007_choose-polling-and-status-behavior.md) (accepted, 2026-11-01)
- [Choose Docker Compose with persistent storage and defer backups](0008_choose-container-deployment-without-backups.md) (accepted, 2026-11-01)
