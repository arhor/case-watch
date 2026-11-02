# Service and operator commands

Use Go 1.27.3. From the repository root, build with `go build -o case-watch ./cmd/case-watch`. Set the variables in [configuration](configuration.md) locally; examples contain synthetic values only. The service does not load `.env` automatically. Provision a key explicitly as described in [credentials](credentials.md), then run `case-watch run`.

Startup validates environment syntax, canonical path separation, restricted file permissions, the database schema and every encrypted credential before constructing network adapters. Keys must be outside the database directory subtree and the repository. The service holds an exclusive advisory lock on `DB_PATH.lock` through database closure. All service and maintenance processes must use the same database path. Do not delete the lock file while a process may hold it; the lock is released by process exit, not by removing its pathname.

After validation, startup recovers uncertain notification reservations, then starts one Telegram long poller, the notification worker and the Warsaw scheduler. All share the same coordinator and authorization policy. No scheduler catch-up occurs at startup. Direct replies honor persisted global sending gates and never acknowledge queued change notifications. Diagnostic messages contain fixed operational categories, without credentials, tokens, stage text or message bodies.

SIGINT or SIGTERM cancels intake and workers, closes coordinator admission, cancels/drains portal and send work, waits for handlers and enrollment work, and closes SQLite before releasing the lock. Unexpected worker/storage failure stops the service with a sanitized error so supervision can restart it. Tracking infrastructure monitoring ignores errors from canceled operations, so an interrupted storage read during shutdown does not itself mark the process as failed. Transaction recovery preserves reserved attempt counts; accepted-but-unrecorded Telegram sends may be duplicated after recovery. An ordinary shutdown does not guarantee completion of each pending delivery.

Stop the service before every database maintenance command, including inspection. Commands refuse a held lock and authenticate all stored credentials before operating. Maintenance requires the database and key paths, without Telegram configuration:

| Command | Purpose |
| --- | --- |
| `case-watch provision-key KEY_PATH KEY_ID` | Explicitly create one new protected keyring; refuse overwrite. No database access. |
| `case-watch queue DB_PATH KEY_PATH RECIPIENT_ID` | Inspect queue counts, attempt metadata, deadlines and gates without printing credentials or notification text. |
| `case-watch resume-recipient DB_PATH KEY_PATH RECIPIENT_ID` | Recover uncertain reservations, then reset the oldest unsent item's cycle and recipient pause after correcting the cause. Preserve lifetime attempts and all queued transitions. |
| `case-watch resume-token DB_PATH KEY_PATH` | Recover uncertain reservations and clear the global token suspension after correcting the token. Preserve rate-limit deadlines and recipient pauses. |
| `case-watch rotate-key DB_PATH KEY_PATH NEW_ID` | Persist retained old/new keys, transactionally rewrite credentials with stable identities, verify, then activate the new key. |

Recipient IDs are positive numeric Telegram user IDs. Recovery does not mark any notification delivered or erase history. Ordinary messages only resume an evidenced block pause; exhausted/permanent queues require operator recovery unless a later block/unblock cycle is observed. Restoring authorization makes retained work eligible subject to existing gates.

Rotation is recoverable at each ordering boundary. Before the database commit, old ciphertext remains decryptable. After commit but before active-ID replacement, the persisted keyring already contains the new decryption key; restarting can still read every record. Retry an interrupted rotation with the same inactive `NEW_ID`. If it already became active, rotation completed; do not retire old keys automatically. Stop and correct missing keys or failed verification rather than resetting the database. A read-only key mount suffices for normal service operation; rotation requires explicit writable access to its protected directory with the service stopped.

No backup creation, restore automation, automatic data reset, deployment or Pi provisioning is provided. Losing the database or required key can require re-enrollment and cannot reconstruct history or undelivered transitions. Live portal checks, actual deployment and Pi resource measurements require separate authorization and evidence.

## Migration transition (2026-11-02)

The golang-migrate release supports fresh databases only. Existing PRAGMA
user_version v1/v2 databases are rejected unchanged. To adopt this release with
existing data, explicitly accept loss and follow the stopped-service reset
procedure; owners must re-enroll. Never reset automatically. New migrations run
during storage.Open, including maintenance, under the existing process lock.
Dirty migration metadata prevents startup; keep the service stopped and inspect
the failed migration before correcting state. No automatic version forcing,
downgrade command, or backup tooling is provided.
