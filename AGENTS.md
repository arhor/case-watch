# Repository Instructions

## Finding context

- Use the [documentation index](docs/README.md) to find references relevant to the task. Read additional documents when the change requires them.
- Before running repository tooling, follow the setup instructions in the [root README](README.md); it also lists verification commands.
- For architecture changes, follow the relevant accepted decisions, including dated amendments, found through the [ADR index](docs/adr/README.md). Use its workflow when proposing or replacing a decision.
- Before running the service, portal probe or maintenance commands, consult the operational and integration guides through the documentation index.
- Treat implementation progress and audit records as historical evidence. Check maintained guides and current code for present behavior.

## Making changes

- Verify code changes with the affected offline tests and applicable checks from the root README. For documentation-only changes, check links and formatting.
- Keep the relevant maintained documentation synchronized with behavior changes. Append dated verification evidence without rewriting historical results.
- Report the checks performed and any remaining limitations. Keep environment-dependent acceptance distinct from offline verification.
