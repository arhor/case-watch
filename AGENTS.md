# Repository Guidelines

## Project Structure & Module Organization

`main.go` is the executable entry point. Application wiring, lifecycle, and startup configuration live in `internal/application`; business logic is under `internal/services`. Telegram commands and timed checks enter through `internal/adapters/inbound/telegram` and `internal/adapters/inbound/schedule`, initiating workflows in the case service. Administration-specific external clients live in `internal/adapters/outbound/clients`, allowing different portals to use different protocols. Database setup, migrations, repositories, and transaction management are under `internal/adapters/outbound/database`; shared domain contracts are in `internal/models`. Tests sit beside production files as `*_test.go`; Gdańsk portal fixtures are in `internal/adapters/outbound/clients/testdata`. Deployment files are `Dockerfile` and `compose.yaml`.

## Build, Test, and Development Commands

- `go build -o case-watch .` builds the local binary with the Go 1.27.3 toolchain.
- `go test -timeout 60s ./...` runs the full unit and integration test suite.
- `go test -race -timeout 90s ./...` repeats tests with race detection, matching CI.
- `go vet ./...` performs the static checks required by CI.
- `go test ./internal/services/... -run TestName` targets a package or named test during development.
- `docker build --platform linux/arm64 -t case-watch .` builds the deployment image.

Set `CASE_WATCH_TELEGRAM_BOT_TOKEN` and `CASE_WATCH_DB_PATH` before running `./case-watch`. The program does not load `.env` automatically.

## Coding Style & Naming Conventions

Go community conventions are optional in this project. Prioritize readability, maintainability, clear intent, and established software engineering practices over conformity to Go idioms. Do not introduce, recommend, or defend a style solely because it is "idiomatic Go." Explain choices through their concrete benefits and tradeoffs, and follow explicit project and maintainer preferences when they differ from community conventions.

Use descriptive names for local variables and parameters rather than single-letter names or cryptic abbreviations. Name method receivers `this`. Use `camelCase` for unexported identifiers and multiword package names, when appropriate; use `PascalCase` for exported identifiers as required by Go's visibility rules. Keep package directories consistent with their package names. Choose clear constructors such as `clock.NewFixedClock` when the package name alone does not identify the implementation.

Keep functions focused and readable. Extract cohesive helpers when they clarify intent or separate responsibilities; avoid long, dense functions and unnecessary boilerplate. Keep error context useful and concise, and never include tokens or other secrets.

Use standard Go formatting (`gofmt -w .`) and tabs as emitted by `gofmt`. Preserve the existing layered boundaries: domain contracts should not depend on adapters, and external API or database details belong in their respective packages.

## Testing Guidelines

Use Go's `testing` package and table-driven tests where multiple cases share setup. Name tests `TestFunction_Scenario` or follow the established package convention. Keep deterministic external-service responses in `testdata`. There is no stated coverage threshold; new behavior and bug fixes should include focused tests. Run normal and race-enabled suites before opening a pull request.

## Commit & Pull Request Guidelines

History primarily follows Conventional Commit prefixes such as `feat:`, `refactor:`, `style:`, and `docs:`. Write imperative, scoped summaries and avoid vague messages like `cleanup`. Pull requests should explain the behavior change, note configuration or migration effects, link relevant issues, and report verification commands. Include screenshots only for user-visible Telegram output or documentation imagery.

## Security & Configuration

Copy `.env.example` only as a local reference; never commit real bot tokens, databases, or `.env` files. Use versioned image tags or immutable digests in Compose deployments, and review SQL migration compatibility before release.
