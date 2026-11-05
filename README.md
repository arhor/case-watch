# Case Watch

Case Watch is a Telegram bot for monitoring administrative case statuses. It checks tracked cases periodically, lets users request a status update, and sends notifications when a status changes.

## Build

Requires Go 1.27.3.

```sh
go build -o case-watch ./cmd/case-watch
```

## Run

Configure the Telegram bot token, database path, and access policy using environment variables. See [the example configuration](.env.example) and [configuration guide](docs/configuration.md).

```sh
./case-watch
```

The application reads environment variables directly; it does not load `.env` files. Portal credentials are currently stored as plaintext in the protected SQLite database while the application remains work in progress. See [operator commands](docs/operations.md) for details.

## Deployment

A [Dockerfile](Dockerfile) and [Compose configuration](compose.yaml) are provided for Linux ARM64. Follow the [deployment guide](docs/deployment.md) to build and configure the image, provision secrets, and manage persistent data and updates.

For architecture, behavior, and additional documentation, see the [documentation index](docs/README.md).
