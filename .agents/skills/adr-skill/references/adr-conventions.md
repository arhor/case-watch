# ADR Conventions (Reference)

## Directory

Store all ADRs directly in `docs/adr/` relative to the repository root. Create it if missing. The index is `docs/adr/README.md`.

## Filename Conventions

Pattern: `{NNNN}_filename.md`, for example `0001_choose-database.md`.

- Start at `0001`, then use one more than the highest existing numeric prefix, padded to four digits; do not reuse gaps.
- Use a lowercase, hyphen-separated title slug after the underscore.
- Keep creation dates in frontmatter rather than filenames.
- `README.md` does not participate in numbering.

## Minimal Sections

At minimum, every ADR must clearly include:

1. **Context**: why the decision exists now, what constraints/drivers apply.
2. **Decision**: what is chosen.
3. **Consequences**: what becomes easier/harder, risks, costs, follow-ups.

For agent-first ADRs, also ensure:

- Constraints are explicit and measurable
- Non-goals are stated
- Follow-up tasks are identified

## Status Values

Track status in YAML front matter:

```yaml
---
status: proposed
date: 2025-06-15
decision-makers: Alice, Bob
---
```

Common statuses:

| Status                        | Meaning                                                       |
| ----------------------------- | ------------------------------------------------------------- |
| `proposed`                    | Under discussion, not yet decided                             |
| `accepted`                    | Decision is active and should be followed                     |
| `rejected`                    | Considered but explicitly not adopted                         |
| `deprecated`                  | Was accepted but no longer applies — explain replacement path |
| `superseded by [title](link)` | Replaced by a newer ADR — always link both ways               |

## YAML Front Matter Fields

| Field             | Required | Description                                              |
| ----------------- | -------- | -------------------------------------------------------- |
| `status`          | Yes      | Current lifecycle state                                  |
| `date`            | Yes      | Date of last status change (YYYY-MM-DD)                  |
| `decision-makers` | Yes      | People who own the decision                              |
| `consulted`       | No       | Subject-matter experts consulted (two-way communication) |
| `informed`        | No       | Stakeholders kept up-to-date (one-way communication)     |

The `consulted` and `informed` fields follow the RACI model and are useful for audit trails in larger teams.

## Mutability

- Prefer appending new information with a date stamp over rewriting existing content.
- If a decision is replaced, create a new ADR and explicitly supersede the old one.
- Status changes and after-action notes are fine to edit in-place.

## Organization

Keep one flat directory and shared numbering sequence in `docs/adr/`. Group decisions by topic in the index if useful.
