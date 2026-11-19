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

CREATE TABLE cooldowns (
    subject_type TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    started_at   INTEGER NOT NULL,
    PRIMARY KEY (subject_type, subject_id)
);
