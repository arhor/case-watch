# Case Watch

A private Telegram bot tracking one Gdańsk administrative case per user. Credentials are encrypted with a separate keyring. Manual checks share persisted cooldowns with Warsaw weekday polling; observed changes enter a durable, ordered notification queue.

Start with the [documentation index](docs/README.md) for the product contract, architecture decisions, implementation notes, operator guides and verification evidence.

Use Go 1.27.3. Before repository tooling, run `nvm use` from this directory; if needed, first run `source "$NVM_DIR/nvm.sh"`.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /private/tmp/case-watch ./cmd/case-watch
```

Tests use synthetic credentials and fake endpoints. The production command contacts real Telegram/portal services: provision the key and configure authorization before starting it. No additional live checks, Pi deployment or resource measurements have been performed. Docker runtime acceptance remains outstanding because the development daemon was unavailable. Backups are intentionally excluded.
