# Documentation index

Use the [root README](../README.md) for the project overview, build and deployment entry points. Agent tooling and verification instructions are in [AGENTS.md](../AGENTS.md).

## Product and architecture

| Document | Read for |
| --- | --- |
| [Product contract](product-contract.md) | Commands, enrollment, removal, checks and notification behavior. |
| [ADR index](adr/README.md) | Accepted architecture decisions, dated amendments and the decision workflow. |

## Implementation boundaries

| Document | Read for |
| --- | --- |
| [Storage](storage.md) | Schema, transactions, ownership, attempt reservations and delivery state. |
| [Telegram](telegram.md) | Adapter lifecycle, command intake and delivery integration. |
| [Portal client](portal-client.md) | RPC integration, request limits and error handling. |

## Configuration and operations

| Document | Read for |
| --- | --- |
| [Configuration](configuration.md) | Environment variables, startup validation and authorization. |
| [Credentials](credentials.md) | Current plaintext WIP storage and retained handling safeguards. |
| [Operations](operations.md) | Service lifecycle and stopped-service maintenance commands. |
| [Deployment](deployment.md) | Container provisioning, persistence, updates and pending runtime checks. |

## Verification evidence

| Document | Read for |
| --- | --- |
| [Implementation progress](implementation-progress.md) | Milestone handoffs, historical command results and remaining acceptance work. |
| [Independent audit](implementation-audit.md) | Requirement-to-evidence mapping, corrected findings and verification limits. |
