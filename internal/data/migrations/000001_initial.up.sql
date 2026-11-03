CREATE TABLE enrollments (
    id                 TEXT    PRIMARY KEY,
    owner_id           INTEGER NOT NULL CHECK (owner_id > 0),
    credential_version INTEGER,
    key_id             TEXT,
    payload            BLOB,
    stage              TEXT,
    checked_at         TEXT,
    deleted_at         TEXT,
    CHECK (
        (
            deleted_at         IS NULL AND
            credential_version IS NOT NULL AND
            key_id             IS NOT NULL AND
            payload            IS NOT NULL AND
            stage              IS NOT NULL AND
            checked_at         IS NOT NULL
        ) OR (
            deleted_at         IS NOT NULL AND
            credential_version IS NULL AND
            key_id             IS NULL AND
            payload            IS NULL AND
            stage              IS NULL AND
            checked_at         IS NULL
        )
    )
);

CREATE UNIQUE INDEX active_enrollment_owner
    ON enrollments (owner_id)
    WHERE deleted_at IS NULL;

CREATE TABLE history (
    sequence       INTEGER PRIMARY KEY AUTOINCREMENT,
    enrollment_id  TEXT    NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
    previous_stage TEXT    NOT NULL,
    stage          TEXT    NOT NULL,
    checked_at     TEXT    NOT NULL
);

CREATE TABLE pending (
    sequence          INTEGER PRIMARY KEY AUTOINCREMENT,
    history_sequence  INTEGER NOT NULL REFERENCES history(sequence) ON DELETE CASCADE,
    recipient_id      INTEGER NOT NULL CHECK (recipient_id > 0),
    status            TEXT    NOT NULL DEFAULT 'pending',
    cycle_attempts    INTEGER NOT NULL DEFAULT 0,
    lifetime_attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt      INTEGER NOT NULL DEFAULT 0,
    reserved_at       INTEGER NOT NULL DEFAULT 0,
    failure           TEXT    NOT NULL DEFAULT '',
    message_id        INTEGER NOT NULL DEFAULT 0,
    UNIQUE (history_sequence, recipient_id)
);

CREATE INDEX pending_recipient_order
    ON pending (recipient_id, sequence);

CREATE TABLE manual_guards (
    owner_id INTEGER PRIMARY KEY,
    started  INTEGER NOT NULL
);

CREATE TABLE case_guards (
    enrollment_id TEXT    PRIMARY KEY REFERENCES enrollments(id) ON DELETE CASCADE,
    started       INTEGER NOT NULL
);

CREATE TABLE recipients (
    recipient_id INTEGER PRIMARY KEY,
    pause        TEXT    NOT NULL DEFAULT '',
    blocked      INTEGER NOT NULL DEFAULT 0
);

CREATE TRIGGER pending_recipient
AFTER INSERT ON pending
BEGIN
    INSERT OR IGNORE INTO recipients (recipient_id) VALUES (NEW.recipient_id);
END;
