---
status: accepted
date: 2026-10-30
decision-makers: Repository maintainer
---

# Adopt architecture decision records

## Context and Problem Statement

Case Watch is being initialized and has no application code or existing decision log. The repository maintainer requested ADR bootstrapping so future architecture choices retain their context. Without a decision log, contributors (human or AI agent) would have to infer intent from code and conversations. This would make it hard to:

- Understand whether a pattern is intentional or accidental
- Know if a past decision still applies or has been superseded
- Avoid relitigating decisions that were already carefully considered

We need a lightweight, version-controlled way to capture decisions where the code lives.

## Decision

Adopt Architecture Decision Records (ADRs) using the MADR 4.0 format, stored in `docs/adr/`.

Conventions:

- One ADR per file, named `{NNNN}_title-with-dashes.md`
- New ADRs start as `proposed`, move to `accepted` or `rejected`
- Superseded ADRs link to their replacement
- ADRs are written to be self-contained — a coding agent should be able to read one and implement the decision without further context
- Keep `docs/adr/README.md` in numeric order and update its statuses when records change
- Accepted records govern related implementation; replacing a decision requires a new record with links in both directions

## Scope and Non-goals

Record consequential choices about architecture, dependencies, interfaces, persistence, and operations when there are meaningful alternatives. Routine fixes and local implementation details do not require ADRs. This bootstrap does not choose an application stack or reconstruct decisions that have not been made.

## Implementation Plan

- Create `docs/adr/README.md` with naming conventions, statuses, workflow, and an index linking this record.
- Store this adoption decision in `docs/adr/0001_adopt-architecture-decision-records.md`.
- Add guidance to `AGENTS.md` to consult relevant accepted ADRs before architecture changes.
- Include context, decision, alternatives, consequences, scope, implementation steps, and checkable verification criteria in future records. Scale the detail to the decision.
- Require no new runtime dependencies, configuration, migrations, or application tests for this documentation setup.

## Verification

- [x] The index links to this record and documents naming, statuses, and lifecycle.
- [x] This record has a status, date, and decision-maker in frontmatter.
- [x] `AGENTS.md` directs contributors to consult relevant ADRs.
- [x] All relative Markdown links in `docs/adr/` resolve to existing files.

## Consequences

* Good, because decisions are discoverable and version-controlled alongside the code
* Good, because new contributors (human or agent) can understand the "why" behind architecture choices
* Good, because the team builds a shared decision log that prevents relitigating settled questions
* Bad, because writing ADRs takes time — though a good ADR saves more time than it costs
* Neutral, because ADRs require periodic review to mark outdated decisions as deprecated or superseded

## Alternatives Considered

* No formal records: Continue making decisions in conversations and code comments. Rejected because context is lost and decisions get relitigated.
* Wiki or Notion pages: Capture decisions outside the repo. Rejected because they drift out of sync with the code and are not version-controlled.
* Lightweight RFCs: More heavyweight process with formal review cycles. Rejected as overkill for most decisions — ADRs can scale up to RFC-level detail when needed.

## More Information

* MADR: <https://adr.github.io/madr/>
* Michael Nygard, "Documenting Architecture Decisions": <https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions>
