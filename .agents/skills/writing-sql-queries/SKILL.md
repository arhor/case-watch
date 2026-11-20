---
name: writing-sql-queries
description: Write, edit, or review SQL formatting anywhere in the case-watch repository, including migrations, SQL embedded in Go, query constants, and test SQL. Applies to all SQL statement kinds; currently covers formatting only.
---

# Writing SQL Queries

Apply these formatting conventions to SQL throughout case-watch, regardless of its file type or location. Keep formatting changes within the requested work and preserve SQL behavior, parameter order, and Go scan order.

## Query layout

- Use uppercase SQL keywords and lowercase `snake_case` identifiers, preserving existing schema names.
- Put each clause on its own line in multiline statements. Keep short column and value lists inline when readable.
- Right-align the first word of clause keywords against the first word of the statement: `SELECT`, `FROM`, `JOIN`, `ON`, `WHERE`, `AND`, and `ORDER` end in the same column. Subsequent words such as `BY` remain to the right. Apply the same pattern to `UPDATE` / `SET` / `WHERE` and `INSERT` / `VALUES` / `ON` / `DO` / `SET`.
- Put each additional predicate on a new line with its leading `AND` or `OR`. Preserve parentheses and logical grouping; indent nested groups and subqueries relative to their enclosing expression.
- For multiline `SET` lists, put the first assignment after `SET`. Put a leading comma on each subsequent line, aligned with the last character of `SET`, so assignment names line up. Use the same leading-comma layout for query expression lists when they need wrapping.
- Use spaces for SQL alignment. Inside Go raw strings, preserve the surrounding Go indentation as a common prefix and use spaces for the SQL's relative alignment. Put multiline SQL on lines between the backticks; `gofmt` does not format SQL inside raw strings.

```sql
SELECT h.sequence, h.tracked_case_id, h.prev_stage, h.curr_state, h.checked_at
  FROM tracked_cases_history AS h
  JOIN tracked_cases AS c
    ON c.id = h.tracked_case_id
 WHERE c.owner_id = ?
   AND c.id = ?
   AND c.deleted_at IS NULL
 ORDER BY h.sequence
```

```sql
UPDATE tracked_cases
   SET deleted_at = ?
     , case_number = NULL
     , password = NULL
     , curr_state = NULL
     , checked_at = NULL
 WHERE owner_id = ?
   AND id = ?
   AND deleted_at IS NULL
```

```sql
INSERT INTO cooldowns (subject_type, subject_id, started_at)
VALUES (?, ?, ?)
    ON CONFLICT (subject_type, subject_id)
    DO UPDATE
   SET started_at = excluded.started_at
```

For nested queries, establish the same keyword alignment within each nested level. Keep `UNION ALL` on its own line between its queries.

## Schema definitions

- Put `CREATE TABLE name (` on the opening line and `);` on its own closing line.
- Indent column definitions and table constraints by four spaces. Put one column or table constraint on each line, using trailing commas between definitions.
- Align column types in one column and inline constraints in another within each table. Keep short inline constraints together, including `PRIMARY KEY`, `NOT NULL`, and `REFERENCES ... ON DELETE ...`.
- For an index, put `CREATE [UNIQUE] INDEX name` on the first line, `ON table (columns)` on the next, and an optional `WHERE` on its own line. Right-align `ON` and `WHERE` with `CREATE` as below.
- Terminate statements in migration files with semicolons and separate statements with one blank line. Single-statement Go query strings may omit the semicolon, matching existing queries.

```sql
CREATE TABLE tracked_cases (
    id          TEXT    PRIMARY KEY,
    owner_id    INTEGER NOT NULL,
    case_number TEXT,
    password    TEXT,
    curr_state  TEXT,
    checked_at  TEXT,
    deleted_at  TEXT
);

CREATE UNIQUE INDEX active_tracked_case_owner
    ON tracked_cases (owner_id)
 WHERE deleted_at IS NULL;
```

For statement forms not illustrated here, extend these keyword, indentation, and list conventions without changing their syntax or semantics. Maintain these conventions when adding or editing SQL throughout the repository, including test fixtures and dynamically assembled statements.
