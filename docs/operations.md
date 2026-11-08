# Service and operator commands

Use Go 1.27.3. From the repository root, build with `go build -o case-watch .`. Set the variables in [configuration](configuration.md) locally; examples contain synthetic values only. The service does not load `.env` automatically. Run it with `case-watch`; it has no command-line interface.

The root entry point calls `application.Run(ctx)`. `internal/application` owns
configuration, explicit dependency wiring, startup validation, runtime supervision
and cleanup; its test overrides and lifecycle helpers are private. See
[ADR 0016](adr/0016_encapsulate-application-bootstrap.md).

Startup validates environment syntax, restricted database permissions and the database schema before constructing network integrations. The service holds an exclusive advisory lock on `DB_PATH.lock` through database closure. Do not delete the lock file while a process may hold it; the lock is released by process exit, not by removing its pathname.

After local validation, startup validates the Telegram token with a bounded `getMe` request. Any validation failure prevents workers from starting. Startup starts one Telegram long poller and the Warsaw scheduler. They share the same coordinator; every enrolled owner is eligible subject to check cooldowns and removal fencing. No scheduler catch-up occurs at startup. Scheduled checks save cache/history silently. Use `/status` to see results. Direct replies honor an in-memory send cooldown and are never queued or retried. Diagnostic messages contain fixed operational categories, without credentials, tokens, stage text or message bodies.

Polling slots are calculated by `github.com/robfig/cron/v3` using `CRON_TZ=Europe/Warsaw 0 8,10,12,14,16,18 * * 1-5`, independently of the host time zone. The scheduler processes cases sequentially and calculates the next slot after completion, skipping slots crossed by a slow run. A wake after the scheduled minute is skipped; cancellation drains active work.

SIGINT or SIGTERM cancels intake and workers, closes coordinator admission, cancels/drains portal and send work, waits for handlers and enrollment work, and closes SQLite before releasing the lock. Unexpected worker/storage failure stops the service with a sanitized error so supervision can restart it. Tracking infrastructure monitoring ignores errors from canceled operations, so an interrupted storage read during shutdown does not itself mark the process as failed. No notification recovery or background delivery runs.

An invalid or revoked Telegram token during polling, sending or credential-message deletion stops the entire service, including portal checks. Diagnostics use a fixed sanitized category. Correct `CASE_WATCH_TELEGRAM_BOT_TOKEN` and restart normally; no resume command is required. See [ADR 0012](adr/0012_fail-fast-on-invalid-telegram-token.md).

No backup creation, restore automation, automatic data reset, deployment or Pi provisioning is provided. Losing the database requires re-enrollment and cannot reconstruct history. Portal credentials are plaintext in SQLite during WIP development; restrict access to the database and its directory. Live portal checks, actual deployment and Pi resource measurements require separate authorization and evidence.

## Migration transition (2026-11-02)

The golang-migrate release supports fresh databases only. Existing PRAGMA
user_version v1/v2 databases are rejected unchanged. To adopt this release with
existing data, explicitly accept loss and follow the stopped-service reset
procedure; owners must re-enroll. Never reset automatically. New migrations run
during storage.Open under the existing process lock.
Dirty migration metadata prevents startup; keep the service stopped and inspect
the failed migration before correcting state. No automatic version forcing,
downgrade command, or backup tooling is provided.

## Notification removal (2026-11-08)

Schema migration 2 runs during normal startup under the database lock. It drops
legacy notification deliveries (including reserved/paused work) and recipient
gates. Existing version 1 enrollments, cache, history and check cooldowns survive;
no re-enrollment is needed. Fresh databases run migrations 1 and 2. The recipient
recovery SQL script is removed. Never run old service binaries against the migrated
database. See [ADR 0018](adr/0018_remove-automatic-notifications.md).

Command replies retain an in-memory rate-limit deadline: positive Telegram
`retry_after`, or 30 seconds otherwise. Restart clears it. Invalid-token failure
still stops the whole service; correct the token and restart.
