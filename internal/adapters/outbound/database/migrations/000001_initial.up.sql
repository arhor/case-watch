CREATE TABLE tracked_cases (
    id                 TEXT    PRIMARY KEY,
    owner_id           INTEGER NOT NULL,
    case_number        TEXT,
    password           TEXT,
    curr_state         TEXT,
    checked_at         TEXT,
    deleted_at         TEXT
);

CREATE UNIQUE INDEX active_tracked_case_owner
    ON tracked_cases (owner_id)
 WHERE deleted_at IS NULL;

CREATE TABLE tracked_cases_history (
    sequence        INTEGER PRIMARY KEY AUTOINCREMENT,
    tracked_case_id TEXT    NOT NULL REFERENCES tracked_cases(id) ON DELETE CASCADE,
    prev_stage      TEXT    NOT NULL,
    curr_state      TEXT    NOT NULL,
    checked_at      TEXT    NOT NULL
);

CREATE TABLE cooldowns (
    subject_type TEXT    NOT NULL,
    subject_id   TEXT    NOT NULL,
    started_at   INTEGER NOT NULL,
    PRIMARY KEY (subject_type, subject_id)
);
