# Case Watch

Case Watch is a Telegram bot for monitoring administrative case statuses. It checks tracked cases periodically, lets users request a status update, and saves status changes in history.

## Build

Requires Go 1.27.3.

```sh
go build -o case-watch .
```

## Run

Configure the Telegram bot token and database path using environment variables. See [the example configuration](.env.example).

```sh
./case-watch
```

The application reads environment variables directly; it does not load `.env` files.

## Deployment

To run with [Docker Compose](compose.yaml), copy the example configuration to `.env`, set your Telegram bot token, then build and start the service:

```sh
cp .env.example .env
docker compose up --build -d
```

Compose passes variables from `.env` into the container.

For unattended Raspberry Pi updates from the `master` branch, see
[automatic updates](scripts/auto-update/README.md).
