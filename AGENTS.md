# Repository Instructions

## Finding context

- Use the [documentation index](docs/README.md) to find references relevant to the task. Read additional documents when the change requires them.
- Before running repository tooling, select the repository Node version with `nvm use` (load nvm with `source "$NVM_DIR/nvm.sh"` if needed). Use Go 1.27.3 for the application; see the [root README](README.md) for build instructions.
- For architecture changes, follow the relevant accepted decisions, including dated amendments, found through the [ADR index](docs/adr/README.md). Use its workflow when proposing or replacing a decision.
- Before running the service or maintenance commands, consult the operational and integration guides through the documentation index.
- Treat implementation progress and audit records as historical evidence. Check maintained guides and current code for present behavior.

## Making changes

- Verify code changes with applicable checks: `go test ./...`, `go test -race ./...`, `go vet ./...`, and a build of the root package (`go build -o /tmp/case-watch .`). For documentation-only changes, check links and formatting.
- Keep the relevant maintained documentation synchronized with behavior changes. Append dated verification evidence without rewriting historical results.
- Report the checks performed and any remaining limitations. Keep environment-dependent acceptance distinct from offline verification.
