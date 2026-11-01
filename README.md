# Case Watch

A private Telegram bot tracking one Gdańsk administrative case per user. Credentials are encrypted with a separate keyring. Manual checks share persisted cooldowns with Warsaw weekday polling; observed changes enter a durable, ordered notification queue.

Read [the product contract](docs/product-contract.md), [configuration](docs/configuration.md), and [operator commands](docs/operations.md). Container setup and the outstanding Docker acceptance checks are in [deployment](docs/deployment.md). [ADRs](docs/adr/README.md) explain the accepted decisions; [implementation progress](docs/implementation-progress.md) records verification evidence.

Use Go 1.27.3. Before repository tooling, run `nvm use` from this directory; if needed, first run `source "$NVM_DIR/nvm.sh"`.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /private/tmp/case-watch ./cmd/case-watch
```

Tests use synthetic credentials and fake endpoints. The production command contacts real Telegram/portal services: provision the key and configure authorization before starting it. No additional live checks, Pi deployment or resource measurements have been performed. Docker runtime acceptance remains outstanding because the development daemon was unavailable. Backups are intentionally excluded.
