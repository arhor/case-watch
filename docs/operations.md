# Service and operator commands

Use Go 1.27.3. From the repository root, build with `go build -o case-watch ./cmd/case-watch`. Set the variables in [configuration](configuration.md) locally; examples contain synthetic values only. The service does not load `.env` automatically. Run it with `case-watch --run`.

Commands require the double-dash spelling shown here. Bare command names are rejected; update existing operator scripts when upgrading.

Startup validates environment syntax, restricted database permissions and the database schema before constructing network integrations. The service holds an exclusive advisory lock on `DB_PATH.lock` through database closure. All service and maintenance processes must use the same database path. Do not delete the lock file while a process may hold it; the lock is released by process exit, not by removing its pathname.

After local validation, startup validates the Telegram token with a bounded `getMe` request. Any validation failure prevents workers from starting. Startup then recovers uncertain notification reservations and starts one Telegram long poller, the notification worker and the Warsaw scheduler. All share the same coordinator and authorization policy. No scheduler catch-up occurs at startup. Direct replies honor the shared in-memory send cooldown and never acknowledge queued change notifications. Diagnostic messages contain fixed operational categories, without credentials, tokens, stage text or message bodies.

SIGINT or SIGTERM cancels intake and workers, closes coordinator admission, cancels/drains portal and send work, waits for handlers and enrollment work, and closes SQLite before releasing the lock. Unexpected worker/storage failure stops the service with a sanitized error so supervision can restart it. Tracking infrastructure monitoring ignores errors from canceled operations, so an interrupted storage read during shutdown does not itself mark the process as failed. Transaction recovery preserves reserved attempt counts; accepted-but-unrecorded Telegram sends may be duplicated after recovery. An ordinary shutdown does not guarantee completion of each pending delivery.

An invalid or revoked Telegram token during polling, sending or credential-message deletion stops the entire service, including portal checks. Diagnostics use a fixed sanitized category. Correct `CASE_WATCH_TELEGRAM_BOT_TOKEN` and restart normally; no resume command is required. A notification explicitly rejected for an invalid token retains its retry budget. Pending work, recipient pauses and notification retry deadlines remain intact. The shared send cooldown resets on restart under [ADR 0013](adr/0013_keep-telegram-send-cooldown-in-memory.md). See [ADR 0012](adr/0012_fail-fast-on-invalid-telegram-token.md).

Stop the service before every database maintenance command, including inspection. Commands refuse a held lock. Maintenance requires the database path without Telegram configuration:

| Command | Purpose |
| --- | --- |
| `case-watch --queue DB_PATH RECIPIENT_ID` | Inspect queue counts, attempt metadata, notification deadlines and recipient gates without printing credentials or notification text. |

Queue inspection omits the shared send cooldown because it exists only in the running worker.

Recipient IDs are positive numeric Telegram user IDs. Recovery does not mark any notification delivered or erase history. Ordinary messages only resume an evidenced block pause; exhausted/permanent queues require operator recovery unless a later block/unblock cycle is observed. Restoring authorization makes retained work eligible subject to existing gates.

Recipient recovery uses [scripts/resume-recipient.sql](../scripts/resume-recipient.sql) instead of an application command. After correcting the delivery failure, stop the service and run from the repository root with an existing database path and a positive numeric Telegram user ID:

```sh
sqlite3 -bail /path/to/case-watch.db \
  -cmd '.parameter init' \
  -cmd '.parameter set @recipient_id 123456789' \
  < scripts/resume-recipient.sql
```

The script atomically clears that recipient's pause/blocked state and resets only the oldest undelivered notification's cycle attempts, retry deadline and failure category. It preserves lifetime attempts, delivery status, history, later queued transitions. Restart the service afterward to process eligible work.

The script rolls back if the recipient ID is missing/invalid or any notification in the database is still reserved. If reservations remain after an interrupted shutdown, start the service to perform normal uncertain-attempt recovery, stop it again, then rerun the script. Recovery can cause duplicate delivery for accepted-but-unrecorded sends. Use `-bail` so SQLite exits immediately on an error. Direct SQL does not acquire the application's advisory lock; keep the service and other maintenance processes stopped throughout. SQLite must already be installed; the database must already exist.

No backup creation, restore automation, automatic data reset, deployment or Pi provisioning is provided. Losing the database requires re-enrollment and cannot reconstruct history or undelivered transitions. Portal credentials are plaintext in SQLite during WIP development; restrict access to the database and its directory. Live portal checks, actual deployment and Pi resource measurements require separate authorization and evidence.

## Migration transition (2026-11-02)

The golang-migrate release supports fresh databases only. Existing PRAGMA
user_version v1/v2 databases are rejected unchanged. To adopt this release with
existing data, explicitly accept loss and follow the stopped-service reset
procedure; owners must re-enroll. Never reset automatically. New migrations run
during storage.Open, including maintenance, under the existing process lock.
Dirty migration metadata prevents startup; keep the service stopped and inspect
the failed migration before correcting state. No automatic version forcing,
downgrade command, or backup tooling is provided.
