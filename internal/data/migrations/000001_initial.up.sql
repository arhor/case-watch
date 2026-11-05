CREATE TABLE enrollments (
    id                 TEXT    PRIMARY KEY,
    owner_id           INTEGER NOT NULL,
    case_number        TEXT,
    password           TEXT,
    stage              TEXT,
    checked_at         TEXT,
    deleted_at         TEXT
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
    recipient_id      INTEGER NOT NULL,
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

CREATE TABLE cooldowns (
    subject_type TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    started_at   INTEGER NOT NULL,
    PRIMARY KEY (subject_type, subject_id)
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

--------------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS scheduled_jobs (
    id           INTEGER NOT PRIMARY KEY AUTOINCREMENT,
    kind         TEXT    NOT NULL,
    payload      BLOB    NOT NULL,
    run_at_ms    INTEGER NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'failed')),
    all_attempts INTEGER NOT NULL DEFAULT 0         CHECK (all_attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 1         CHECK (max_attempts >= 1),
    repeat_ms    INTEGER NOT NULL DEFAULT 0         CHECK (repeat_ms >= 0),
    last_error   TEXT,
    created_ms   INTEGER NOT NULL,
    updated_ms   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS scheduled_jobs_due
    ON scheduled_jobs (run_at_ms, id)
 WHERE status = 'pending';
