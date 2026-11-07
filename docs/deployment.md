# Docker deployment

This is an operator runbook, not evidence of a deployment. Use one Linux ARM64 container on a 64-bit host. No live portal/Telegram checks or Pi provisioning have been performed. Read [operations](operations.md) and [configuration](configuration.md) first. There is no backup/restore automation.

Portal case numbers and passwords are currently plaintext in SQLite. Protect the named volume and host access accordingly. Loss of the volume loses enrollment, history and pending notifications.

Build from the repository root:

```sh
docker buildx build --platform linux/arm64 --load -t case-watch:2026-11-05 .
```

Use an explicit version tag or immutable registry digest; do not use `latest`. The image runs as UID/GID 10001 and mounts the named `case-watch-data` volume at `/data`. Keep that volume private and never share it with a second poller.

Set the image and Telegram token in the operator shell or a protected environment file outside the repository. Compose configuration output can reveal supplied secrets.

```sh
export CASE_WATCH_IMAGE=case-watch:2026-11-05
# Set CASE_WATCH_TELEGRAM_BOT_TOKEN privately.
docker compose up -d
```

## Lifecycle and updates

```sh
docker compose logs --tail 100 case-watch
docker compose stop case-watch
docker compose start case-watch
```

`unless-stopped` restarts failed running containers. Docker sends SIGTERM directly to Go and allows 90 seconds before forced termination. Normal cancellation drains work and closes SQLite; uncertain accepted sends can repeat after forced termination. Missed schedule slots are skipped.

For an update, build or pull the next version, stop the existing service, change `CASE_WATCH_IMAGE`, then run:

```sh
docker compose up -d --no-deps --force-recreate case-watch
```

Keep the exact volume name. Do not run old and new pollers concurrently. Startup rejects incompatible schema and invalid configuration; correct the cause instead of deleting data automatically.

Recipient recovery continues to use the stopped-service SQL procedure documented in [operations](operations.md).

## Accepted loss and validation

Container removal alone does not remove the named volume. Do not use `down -v` as routine maintenance. Because this application is not deployed and has no existing databases, the WIP schema is revised in place and development databases are recreated explicitly when needed.

Docker lifecycle behavior remains unverified in this development environment. Before deployment, use synthetic cases and fake portal/Telegram transports to verify persistence across container recreation, normal and forced termination, invalid startup refusal and explicit loss/re-enrollment. Run `docker version`, the build command above and `docker compose config --quiet` with synthetic settings before those checks.
