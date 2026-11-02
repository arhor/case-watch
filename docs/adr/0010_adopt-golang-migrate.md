---
status: proposed
date: 2026-11-02
decision-makers: Repository maintainer
---

# Adopt golang-migrate for database migrations

## Context and decision

Replace custom migration sequencing with golang-migrate v4.20.1, its modernc
SQLite driver, and embedded SQL through iofs. This supplements [ADR 0004](0004_choose-sqlite-persistence.md);
the database driver, durability settings, storage boundary and process lock remain.

The maintainer selected fresh databases only. Consolidate the existing complete
schema into migration 1. Reject legacy user_version databases without changing
them; resetting remains an explicit operator action. No legacy conversion or
downgrade command is provided.

## Alternatives and consequences

Keeping custom sequencing avoids a dependency but retains migration machinery.
Bridging legacy metadata preserves existing data but adds compatibility code the
maintainer explicitly declined. Embedded migration files travel with the binary.

SQL execution is transactional per migration. Dirty markers commit separately;
failed or interrupted migrations require operator correction. Never force a version
automatically. The SQLite adapter lock is not a cross-process lock.

## Implementation and verification

- Embed sequential immutable up SQL files and invoke Up during storage.Open.
- Validate metadata before constructing the adapter; reject unknown or dirty schemas.
- Preserve connection ownership and cancellation-safe cleanup.
- Include SQL in the Docker context and document explicit legacy rejection.
- Verify fresh initialization, reopen, data retention, incompatible-schema refusal,
  transactional failure, Go checks, and CGO-disabled Linux ARM64 builds.

This record remains proposed pending the repository decision acceptance workflow.
