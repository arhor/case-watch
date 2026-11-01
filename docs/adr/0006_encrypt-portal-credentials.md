---
status: accepted
date: 2026-11-01
decision-makers: Repository maintainer
---

# Encrypt portal credentials with a separately stored key

## Context and Problem Statement

Case Watch must recover case numbers and portal passwords to perform unattended checks. Hashing alone cannot support that requirement. [ADR 0004](0004_choose-sqlite-persistence.md) selects SQLite with modernc and [ADR 0005](0005_choose-user-authorization-and-enrollment.md) establishes private enrollment and case ownership; both defer credential protection.

The maintainer requires automatic recovery after restarts and power outages without manual unlocking. Protection against database or database-backup leakage is sufficient. Protection against full compromise of the running Pi or theft of both the database and locally stored key is outside the selected threat model.

## Decision Drivers

- Keep portal credentials unreadable in database-only copies and backups.
- Support unattended startup and the existing modernc choice.
- Work with direct execution, service managers, or containers without requiring any one deployment method.
- Use standard authenticated cryptography and keep key backup and rotation explicit.

## Considered Options

- Application-level authenticated encryption with a separate local key file.
- SQLCipher whole-database encryption.
- Filesystem or disk encryption.
- External secrets/key service or hardware-backed keys.
- Plaintext storage protected only by filesystem permissions.

## Decision Outcome

Encrypt each credential record's case number and portal password together using AES-256-GCM from Go's standard library. Use a cryptographically random 32-byte key, not a user password, portal password, bot token, or value derived from a case number. Supply the key through a protected local file whose path is configured using `CASE_WATCH_KEY_FILE`.

This is field encryption, not full-database encryption. Case stages, history, opaque identifiers, recipient associations, and other metadata remain readable in database copies. No claim is made that the database as a whole is confidential.

### Encryption and record binding

- Use `crypto/aes` and `crypto/cipher.NewGCMWithRandomNonce`. This standard-library API generates and prefixes a fresh random nonce for each encryption; do not implement a custom nonce generator or reuse a nonce. Observe its limit of fewer than 2^32 encryptions per key.
- Store an encryption-format version, key identifier, and sealed payload containing the nonce, ciphertext, and authentication tag. The decrypted payload holds the case number and password. Do not store a plaintext duplicate or plaintext search index of those values.
- Bind encryption to the immutable credential-record identity using authenticated associated data (AAD). Include an application-purpose discriminator, encryption-format version, key identifier, and credential-record ID in an unambiguous encoding. Decryption must reconstruct this context from the destination record rather than trusting context copied with the ciphertext.
- Copying a sealed payload to another record, changing authenticated metadata, tampering, a wrong key, or an unsupported format must produce an error rather than credentials or a portal request. Authentication failure does not imply that the government portal rejected the password.
- Decrypt only when required for authenticated application operations or portal checks. Keep plaintext out of SQL parameters, database-backed enrollment drafts, pending-notification payloads, logs, error messages, and test fixtures. Use opaque identifiers for persistent references; any display formatting requiring a case number happens in memory after authorization.

### Key provisioning and startup

- The key file contains a versioned JSON keyring: a file-format version, one active key ID, and a map from key IDs to base64-encoded 32-byte keys. Initially provision one active key. This permits retaining old decryption keys during rotation without placing key material in SQLite.
- Generate keys with `crypto/rand` through an explicit operator provisioning action. Do not print key material to logs, bake it into executables/images, commit it, or automatically replace a missing key at service startup.
- Keep the key file outside the repository, database directory, and database backup location. Restrict the file to the service account (Unix mode `0600`) and its containing directory to authorized access. Configuration contains only its path, not the key value. A container may receive the same file through a read-only mount.
- Load and validate the keyring on startup; reject missing/unreadable files, unsupported formats, duplicate or empty key IDs, invalid encoding, wrong key lengths, and an absent active key. Reject references to unavailable keys. Verify existing credential records can be authenticated before enabling enrollment, polling, or checks; never silently discard undecryptable records or reset their keys.
- Keep the loaded keys in process memory. Apply key-file changes on restart; no live reload is required. Go memory handling does not guarantee that all historical plaintext/key copies are securely erased.

### Backup, recovery, and rotation

- Database backups contain encrypted credential payloads and no key material. Back up the keyring separately in a restricted location, not in the same archive or alongside publicly accessible database backups. Document both recovery inputs.
- Losing all copies of a required key makes the corresponding credentials unrecoverable; users must enroll them again. Re-enrollment is recovery from key loss, not a mechanism for decrypting old backups.
- Rotate through an explicit maintenance operation with the service stopped: back up the database and keyring separately, generate a new random key under a new ID, retain old keys, and re-encrypt credential records transactionally using their existing identities and fresh nonces. Activate the new key for subsequent writes and verify all records before restarting.
- Persist the keyring containing both old and new keys safely before committing any ciphertext under the new key. Keep the old key active until re-encryption commits, then persist the new active ID. Interrupted rotation must leave every committed record decryptable; this ordering avoids depending on an atomic update across SQLite and the key file.
- Retain the previous keyring until rotation and restore checks pass. Retain old keys for as long as backups requiring them must remain recoverable. Removing an old key is a deliberate retirement action, never an automatic cleanup tied only to current rows.

### Consequences

- Database-only leakage does not reveal the case numbers or portal passwords without access to the key; other database metadata remains exposed.
- The runtime can restart unattended without a remote service or operator passphrase.
- Key provisioning, separate backup, and rotation become operator responsibilities. Backing up only the database is insufficient for credential recovery.
- Anyone controlling the running service or obtaining both key and database can recover credentials. The key file on the same device is compatible with the explicitly selected threat model.
- The standard-library crypto adds no third-party encryption dependency and preserves modernc deployment flexibility. Resource costs remain unmeasured.

## Scope and Non-goals

This decision covers portal credential encryption at rest and its local encryption keys. It does not encrypt the whole SQLite database, select Telegram-token provisioning, protect against host compromise, or provide end-to-end encrypted Telegram enrollment. Handling/deleting credential-bearing Telegram messages and enrollment conversation recovery remain separate feature-design work; application encryption does not remove copies held by the messaging platform. This record supplements existing ADRs without superseding them.

## Implementation Plan

These tasks are pending; this record creates no real keys and adds no runtime code or dependencies.

- [ ] Implement a standard-library encryption/keyring boundary under `internal/credentials/`, referencing this ADR and keeping SQL and Telegram types outside it.
- [ ] Add `CASE_WATCH_KEY_FILE` parsing and startup validation under `internal/config/`; provide explicit key provisioning and maintenance rotation tooling without logging secrets.
- [ ] Add encrypted credential records and their versioned migrations under `internal/storage/`. Use immutable opaque record IDs and bind AAD to the destination identity. Do not introduce plaintext credential columns or persistent drafts.
- [ ] Integrate authorized enrollment and portal access with encryption/decryption, and prevent raw payloads from reaching diagnostics or pending-notification storage.
- [ ] Document key-file permissions, unattended startup, separate backups, restore, transactional rotation, retention of old keys, and recovery by re-enrollment after key loss.
- [ ] Test with synthetic credentials, temporary key files, and temporary databases; no live Telegram or government-portal requests are required.

### Verification

- [ ] Round-trip encryption recovers synthetic credentials; repeated encryption of the same payload produces different sealed values.
- [ ] Tampering, wrong keys, changed record IDs, altered authenticated metadata, truncated payloads, and unsupported versions fail without returning credentials or making portal requests.
- [ ] Missing/invalid key files, unknown key IDs, and existing-record authentication failures stop startup before network work; startup never creates replacement keys.
- [ ] Database files, journals, backups, draft records, notification records, and diagnostics contain no synthetic plaintext credentials after enrollment and updates.
- [ ] Restart loads the existing keyring unattended and decrypts stored records without user input.
- [ ] Restore succeeds with the matching separately backed-up keyring and fails clearly without required keys.
- [ ] Rotation preserves record identity, replaces ciphertext with fresh nonces transactionally, supports rollback/recovery, and leaves old backups decryptable with retained keys.
- [ ] Crypto/configuration tests and Linux ARM64 builds succeed with CGO disabled and no additional encryption dependency.

## Pros and Cons of the Options

### Field encryption and local key file

Meets database-leak protection and unattended startup with the standard library and modernc. Chosen for simplicity. Requires careful binding, secret handling, and key lifecycle work; it does not protect readable metadata or defend against possession of both key and database.

### SQLCipher

Protects database pages transparently, including more metadata. Not chosen because it requires revisiting the selected SQLite build/driver while leaving key delivery and backup responsibilities unresolved.

### Filesystem or disk encryption

Can protect more files when the device is locked. Not selected as the primary mechanism because manual unlocking conflicts with unattended recovery, and exported database files/backups require their own protection. It may be added independently later.

### Remote or hardware-backed keys

Can separate key access from disk possession and centralize controls. Not chosen because additional availability dependencies, hardware, and recovery procedures are unnecessary for the selected threat model.

### Plaintext plus permissions

Simple and supports unattended operation. Rejected because readable database copies directly reveal credentials, contrary to the selected requirement. Password hashes alone are also insufficient because the portal requires the original password.

## More Information

Sources reviewed on 2026-11-01:

- [Go authenticated encryption and random-nonce GCM](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce).
- [Go cryptographic randomness](https://pkg.go.dev/crypto/rand).
- [SQLCipher design](https://www.zetetic.net/sqlcipher/design/).
- [OWASP secrets-management guidance](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html).

The maintainer confirmed the proposed model after selecting unattended recovery and database/backup-leak protection. Revisit key provisioning if the threat model expands to whole-device theft, shared hosts, or remote administration. Implementation and validation remain pending.
