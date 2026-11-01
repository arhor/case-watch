---
status: accepted
date: 2026-11-01
decision-makers: Repository maintainer
---

# Choose Docker Compose with persistent storage and defer backups

## Context and Problem Statement

Case Watch needs to restart automatically and preserve local state across application updates on a Raspberry Pi 5 running 64-bit Raspberry Pi OS. The maintainer proposed Docker with persistent storage and explicitly accepts losing local data because the current case stage can be fetched again from the government portal.

The portal cannot reconstruct local transition history, recipient associations, enrollment data, or pending notifications. The maintainer accepts manual re-enrollment and potentially missed or repeated notifications after data loss. Automatic restart and persistence are required; backup and restore infrastructure are not required initially.

[ADR 0004](0004_choose-sqlite-persistence.md) selects SQLite, [ADR 0006](0006_encrypt-portal-credentials.md) selects a separate local encryption-key file, and [ADR 0007](0007_choose-polling-and-status-behavior.md) defines polling behavior after restart. Earlier backup requirements in ADRs 0004 and 0006 need an explicit scoped amendment.

## Decision Drivers

- Keep deployment repeatable and simple for one personal-service instance.
- Restart automatically after process failure or host reboot.
- Preserve SQLite and encryption keys across container replacement.
- Avoid backup infrastructure while local data loss is acceptable.

## Considered Options

- Docker Compose with a named volume.
- Docker Compose with a bind-mounted database directory.
- Native Go executable supervised by systemd.

## Decision Outcome

Deploy one Linux ARM64 service container through Docker Compose. Use a Docker-managed named volume mounted at `/data` for SQLite and its companion files, with `CASE_WATCH_DB_PATH=/data/case-watch.db`.

Run the process as a non-root user and provision volume ownership for that identity. Keep the encryption-key file outside the database volume, repository, and image. Mount it separately and read-only, preserving the service-account access restrictions in ADR 0006, and configure its container path through `CASE_WATCH_KEY_FILE`. Provision the key explicitly before first startup; never regenerate it during container recreation or after a missing-key error.

### Restart and lifecycle

- Configure `restart: unless-stopped` and enable Docker to start at host boot. A deliberately stopped container remains stopped across Docker/host restarts until explicitly started.
- Use an exec-form container entry point so the Go process receives termination signals. Handle shutdown through context cancellation: stop accepting new work, stop scheduling, finish or cancel in-flight operations, and close SQLite before exiting.
- Use a 90-second container stop grace period initially. Forced termination remains possible if shutdown exceeds the period; restart must rely on transaction recovery rather than assuming all operations completed.
- Do not add a second process supervisor inside the container. Restart policies handle process exits; detecting and recovering from a running but unresponsive process is outside this initial decision.
- Use versioned image tags and explicitly replace the service container for upgrades, retaining the named volume and key-file mount. Do not run old and new pollers concurrently. Existing schema-version checks must reject incompatible executables rather than automatically resetting data.
- Preserve the missed-schedule and pending-work behavior in ADRs 0004 and 0007 across ordinary restarts. Persistence does not require replaying missed polling slots.

### No backups initially

Do not implement scheduled database backups, off-Pi copies, automated restore, key backups, retention jobs, or mandatory pre-upgrade backups in this initial deployment. Persistent storage is not a backup; loss of the Pi storage, explicit volume deletion, key loss, or a failed migration may require rebuilding state and re-enrolling cases.

This intentionally defers the backup creation, separate key-backup, pre-upgrade backup, backup-retention, and backup/restore verification requirements previously described in ADRs 0004 and 0006. Their other requirements remain active: encryption, key separation, safe rotation ordering, transactions, migrations, startup validation, and explicit schema checks still apply. Do not silently delete or reset unreadable databases or undecryptable records; recovery by reset must be an explicit operator action.

Retain keys required by existing encrypted records and keep old keys through rotation verification. Rotation still follows ADR 0006's ordering, persisting both old and new keys before committing new ciphertext; no new backup copies are required under this amendment. Losing an old key can also make any independently retained old database copy unrecoverable, an accepted consequence of the initial no-backup policy.

### Consequences

- Docker owns the database storage location, avoiding host-directory layout and backup scripts.
- Container replacement preserves data if the same named volume and key file are reused. Changing volume names or deleting volumes is not a recovery mechanism.
- The host requires Docker and Compose; a native systemd service would avoid that runtime infrastructure but is not selected.
- No recovery guarantee is provided for host-storage loss. Fresh enrollment can fetch the current portal stage but cannot reconstruct local history or undelivered transitions.
- Restart policies do not fix invalid configuration, missing keys, or broken migrations; diagnostics and operator correction remain necessary.
- Resource consumption of the running bot and Docker has not been measured on the target Pi.

## Scope and Non-goals

This record selects the deployment shape, restart policy, persistence placement, and intentional absence of backups. It does not add deployment artifacts, provision the Pi, choose an image registry or CI platform, implement uptime monitoring, or promise public-service reliability. Prior technology and authorization decisions remain accepted. The dated notes in ADRs 0004 and 0006 link to this scoped amendment rather than superseding those decisions in full.

## Implementation Plan

These tasks remain pending:

- [ ] Add a multi-stage `Dockerfile` producing a Linux ARM64 image from the modernc-based service, running as a non-root user with CA certificates and Warsaw time-zone data available.
- [ ] Add `compose.yaml` defining one service, `restart: unless-stopped`, `stop_grace_period: 90s`, the named data volume, and the separately provisioned read-only key-file mount. Exclude real secrets, raw captures, and local data from the build context and committed examples.
- [ ] Add signal-driven shutdown to the eventual service entry point and ensure SQLite is closed. Do not add backup dependencies, timers, jobs, or restore tooling for this phase.
- [ ] Document first provisioning, host Docker boot setup, start/stop, log inspection, versioned updates, volume/key reuse, explicit reset/re-enrollment, and accepted data-loss consequences in `docs/deployment.md`.
- [ ] Validate container lifecycle behavior using synthetic cases and keys and a fake portal/Telegram endpoint, before deploying real credentials or allowing live checks.

### Verification

- [ ] The image targets Linux ARM64 and the process runs as non-root with writable data storage, read-only key access, TLS certificates, and Warsaw time-zone data.
- [ ] Recreating the container with the same volume and key preserves synthetic credentials, stage/history, cooldowns, and pending notifications.
- [ ] Process failure and Docker/host restart resume an intentionally running service; an explicitly stopped service remains stopped under `unless-stopped`.
- [ ] Termination reaches the Go process, exits within the grace period in normal operation, and leaves SQLite reopenable; forced-termination recovery respects transaction boundaries.
- [ ] Image replacement does not start two pollers or reset storage; incompatible schema, missing keys, and invalid configuration fail clearly.
- [ ] Synthetic loss/re-enrollment scenarios confirm the documented recovery limitations without live portal or Telegram access.
- [ ] No backup automation or real secrets are included in deployment artifacts or images. Pi resource suitability is reported only after target-device measurement.

## Pros and Cons of the Options

### Docker Compose with named volume

Chosen because the maintainer favors containers and Docker-managed persistence. Reproducible service configuration and image replacement are straightforward. Requires Docker infrastructure and volume-aware inspection; it does not protect against host-storage loss.

### Docker Compose with database bind mount

Provides an explicit host directory convenient for inspection and host-side backups. Not chosen because backups are deferred and host-directory layout/ownership adds work without a current need. The key file remains a separate read-only mount regardless of the database volume choice.

### Native executable with systemd

Avoids Docker infrastructure while offering boot startup and restart supervision. Not chosen because the maintainer prefers a container workflow; it remains technically viable with the selected Go/modernc stack.

## More Information

- [Docker restart policies](https://docs.docker.com/engine/containers/start-containers-automatically/).
- [Docker volume lifecycle](https://docs.docker.com/engine/storage/volumes/).
- [systemd service supervision](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html).

Confirmed on 2026-11-01. Revisit backup policy if reconstruction becomes burdensome, historical data gains importance, or the service serves more people. Implementation and target-device validation remain pending.
