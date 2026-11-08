# Portal credential storage

Credential encryption is deferred while Case Watch remains an undeployed work-in-progress application with no existing databases. See [ADR 0014](adr/0014_defer-credential-encryption-during-wip.md), which supersedes ADR 0006.

The enrollment case number and portal password are stored as plaintext columns in the mode-0600 SQLite database. Access to the database or a copy grants access to those credentials. Restrict the database directory to the service account and do not publish, attach or commit database files.

This does not relax handling outside storage: credentials must not appear in logs, errors, replies or history. Telegram still collects the number and password separately, keeps unfinished drafts in memory, expires them after ten minutes and attempts to delete credential-bearing messages.

Revisit encryption before deployment or when a concrete database-disclosure threat model requires it. There is currently no keyring, encryption environment variable, provisioning command or rotation command.
