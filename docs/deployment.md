# Docker deployment

This is an operator runbook, not evidence of a deployment. Use one Linux ARM64 container on a 64-bit host. No live portal/Telegram checks or Pi provisioning have been performed. Read [operations](operations.md), [configuration](configuration.md), and [credentials](credentials.md) first. There is no backup/restore automation. Loss of storage or required keys can lose enrollment, history and pending notifications; the portal supplies only current stages.

Build from the repository root:

```sh
docker buildx build --platform linux/arm64 --load -t case-watch:2026-11-01 .
```

Use an explicit version tag or immutable registry digest for every release; do not use `latest` in production. The multi-stage image uses Go 1.27.3, CGO-disabled modernc SQLite, a dedicated UID/GID 10001, trusted CA certificates and embedded Go Warsaw timezone rules. Its exec entrypoint runs the Go binary directly, without a root bootstrap, shell or supervisor. The build context is an allowlist of production Go sources and module files. Keep secrets and raw captures outside those source paths as well as outside the image.

## First provisioning

Install Docker Engine and Compose on the host and explicitly enable its system service at boot (`sudo systemctl enable --now docker` on a systemd host). Select a host directory outside the repository and data volume for the persistent key. UID/GID 10001 must be suitable on that host; a matching named host service account is optional. Never make the key world-readable to solve access errors.

```sh
sudo install -d -m 0700 -o 10001 -g 10001 /var/lib/case-watch-keys
docker run --rm --platform linux/arm64 --user 10001:10001 \
  --mount type=bind,src=/var/lib/case-watch-keys,dst=/keys \
  case-watch:2026-11-01 provision-key /keys/keyring.json initial
```

This explicit command creates a random mode-0600 key, refuses overwrite, and performs no network or database work. Do not provision again during container replacement. The host directory remains mode 0700, owned by UID/GID 10001; the file must also be owned by that identity. Normal Compose operation mounts only that existing file read-only into the image's private mode-0700 `/keys` directory. Rootless Docker or user-namespace remapping needs equivalent mapped host ownership, verified before startup.

Set these variables in the operator shell or a protected environment file outside the repository (Compose supports `--env-file`; the application itself does not load dotenv files):

```sh
export CASE_WATCH_IMAGE=case-watch:2026-11-01
export CASE_WATCH_HOST_KEY_FILE=/var/lib/case-watch-keys/keyring.json
# Set CASE_WATCH_TELEGRAM_BOT_TOKEN and CASE_WATCH_ALLOWED_USER_IDS privately.
# Leave CASE_WATCH_ALLOW_ALL_USERS absent for restricted mode.
```

Do not paste tokens into terminal transcripts, diagnostic tickets or committed files. Compose interpolation/configuration output can reveal them. The configuration deliberately refuses a missing host key rather than creating a directory at that path.

The named volume `case-watch-data` mounts at `/data`. On its first use Docker copies the image's UID/GID-10001, mode-0700 directory metadata into the empty volume; do not disable volume copy-up. Existing volumes are not repaired automatically. Verify ownership on the actual Docker installation before using real credentials. Never share this volume with a second poller or change the configured database path to bypass its process lock.

## Lifecycle and updates

```sh
docker compose up -d
docker compose logs --tail 100 case-watch
docker compose stop case-watch
docker compose start case-watch
```

`unless-stopped` restarts failed running containers and resumes them after Docker/host restart. An explicitly stopped container stays stopped until started. Docker sends SIGTERM directly to Go and allows 90 seconds before forced termination. Normal cancellation drains work and closes SQLite; uncertain accepted sends can repeat after forced termination. Missed schedule slots are skipped.

For an update, build or pull the next version first, stop the existing service, set `CASE_WATCH_IMAGE` to that version, then run `docker compose up -d --no-deps --force-recreate case-watch`. Keep the exact volume name and host key path. Do not use a second Compose project/service to run the new image alongside the old poller. Startup rejects incompatible schema, missing keys, invalid configuration and undecryptable records; correct the cause, never delete data automatically. An older executable may reject a newer schema, so rollback is not guaranteed.

## Stopped-service maintenance

Stop the service before inspection, resumption or rotation. For ordinary maintenance `docker compose run --rm --no-deps case-watch queue /data/case-watch.db /keys/keyring.json RECIPIENT_ID` reuses the read-only key file and named volume; substitute the documented resume commands as needed. Explicitly restart afterward.

Rotation atomically replaces the key file. A file bind mount pins the old inode, so do not rotate through the normal file mount or start an old container after host-side replacement. With the service stopped, mount the protected key **directory** read-write for one maintenance invocation:

```sh
docker compose stop case-watch
docker run --rm --platform linux/arm64 --user 10001:10001 \
  --mount type=volume,src=case-watch-data,dst=/data \
  --mount type=bind,src=/var/lib/case-watch-keys,dst=/keys \
  "$CASE_WATCH_IMAGE" rotate-key /data/case-watch.db /keys/keyring.json next-id
docker compose up -d --no-deps --force-recreate case-watch
```

On failure keep the service stopped and follow the interrupted-rotation guidance in operations; retry the same inactive ID. Retain all old keys. Recreating the service ensures the read-only file mount points to the replacement inode.

## Accepted loss and explicit reset

Container removal alone does not remove the named volume or host key. Do not use `down -v` as routine maintenance. If an operator explicitly accepts loss of all local enrollment, history and pending transitions, stop and remove the service, explicitly remove `case-watch-data`, and recreate the service with the existing key. Owners must enroll again; past transitions cannot be reconstructed. If the key itself is lost, do not generate a replacement against the old database: an explicit empty-state reset and separately provisioned new key are required. No reset command is run automatically or as part of upgrades.

## Validation still required on Docker

The development environment currently has no reachable Docker daemon. Image build, runtime UID/certificate/timezone checks, volume copy-up, file mount permissions, container recreation, signal delivery, restart policy and host boot behavior remain unverified. Linux ARM64 cross-compilation and offline Go tests do not establish these properties. Before deployment, use synthetic keys/cases and fake portal/Telegram transports to verify retained encrypted credentials, history, cooldowns, queue/gates across recreation; normal and forced termination with SQLite reopen; refusal of invalid startup; and explicit loss/re-enrollment. Do not connect production endpoints for those checks. The current production CLI deliberately has no endpoint override; an offline container harness must inject test factories/build a test binary rather than passing fake-looking production tokens. Run `docker version`, the build command above, and `docker compose config --quiet` with synthetic settings before lifecycle checks. Confirm the image user/entrypoint with `docker image inspect`, and inspect the created volume and file access as UID 10001. Perform Docker/host reboot checks only in an authorized disposable environment. Pi resource suitability requires later target-device measurement.
