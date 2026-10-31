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
