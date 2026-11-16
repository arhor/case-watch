import { DatabaseSync } from "node:sqlite";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fail } from "./common.js";
export class Storage {
  constructor(path) {
    this.db = new DatabaseSync(resolve(path));
    try {
      this.db.exec(
        "PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;",
      );
      const version = this.db.prepare("PRAGMA user_version").get().user_version;
      if (version > 1) throw fail("storage");
      if (version === 0)
        this.tx(() => {
          this.db.exec(
            readFileSync(
              new URL("../migrations/001.sql", import.meta.url),
              "utf8",
            ),
          );
          this.db.exec("PRAGMA user_version=1");
        });
    } catch {
      this.db.close();
      throw fail("storage");
    }
  }
  tx(fn) {
    this.db.exec("BEGIN IMMEDIATE");
    try {
      const result = fn();
      this.db.exec("COMMIT");
      return result;
    } catch (error) {
      this.db.exec("ROLLBACK");
      throw error;
    }
  }
  get(owner, id) {
    const row = this.db
      .prepare(
        "SELECT id, owner_id AS owner, case_number AS number, password, stage, checked_at AS checkedAt FROM enrollments WHERE owner_id=? AND deleted_at IS NULL",
      )
      .get(owner);
    if (!row || (id && row.id !== id)) throw fail("not_found");
    return row;
  }
  owners() {
    return this.db
      .prepare(
        "SELECT owner_id FROM enrollments WHERE deleted_at IS NULL ORDER BY owner_id",
      )
      .all()
      .map((r) => r.owner_id);
  }
  create(e) {
    this.tx(() => {
      if (
        this.db
          .prepare(
            "SELECT 1 FROM enrollments WHERE id=? OR (owner_id=? AND deleted_at IS NULL)",
          )
          .get(e.id, e.owner)
      )
        throw fail("exists");
      this.db
        .prepare(
          "INSERT INTO enrollments(id,owner_id,case_number,password,stage,checked_at) VALUES(?,?,?,?,?,?)",
        )
        .run(e.id, e.owner, e.number, e.password, e.stage, e.checkedAt);
    });
  }
  observe(owner, id, stage, checkedAt) {
    this.tx(() => {
      const previous = this.get(owner, id);
      this.db
        .prepare("UPDATE enrollments SET stage=?,checked_at=? WHERE id=?")
        .run(stage, checkedAt, id);
      if (previous.stage !== stage)
        this.db
          .prepare(
            "INSERT INTO history(enrollment_id,previous_stage,stage,checked_at) VALUES(?,?,?,?)",
          )
          .run(id, previous.stage, stage, checkedAt);
    });
  }
  eligibility(owner, id, manual) {
    this.get(owner, id);
    const start = (type, subject) =>
      this.db
        .prepare(
          "SELECT started_at FROM cooldowns WHERE subject_type=? AND subject_id=?",
        )
        .get(type, String(subject))?.started_at;
    const starts = [
      start("case_check", id),
      ...(manual ? [start("manual_check", owner)] : []),
    ].filter((x) => x !== undefined);
    return starts.length ? Math.max(...starts) + 60000 : 0;
  }
  reserve(owner, id, manual, now) {
    return this.tx(() => {
      const next = this.eligibility(owner, id, manual);
      if (now < next) return { reserved: false, nextEligibleAt: next };
      const put = (type, subject) =>
        this.db
          .prepare(
            "INSERT INTO cooldowns VALUES(?,?,?) ON CONFLICT(subject_type,subject_id) DO UPDATE SET started_at=excluded.started_at",
          )
          .run(type, String(subject), now);
      put("case_check", id);
      if (manual) put("manual_check", owner);
      return { reserved: true, nextEligibleAt: now + 60000 };
    });
  }
  remove(owner, id, now) {
    this.tx(() => {
      this.get(owner, id);
      this.db
        .prepare(
          "UPDATE enrollments SET deleted_at=?,case_number=NULL,password=NULL,stage=NULL,checked_at=NULL WHERE id=?",
        )
        .run(now, id);
      this.db.prepare("DELETE FROM history WHERE enrollment_id=?").run(id);
      this.db
        .prepare(
          "DELETE FROM cooldowns WHERE subject_type='case_check' AND subject_id=?",
        )
        .run(id);
    });
  }
  close() {
    this.db.close();
  }
}
