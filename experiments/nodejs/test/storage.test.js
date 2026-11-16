import test from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./support.js";
test("migration pragmas, restart persistence, history and deletion semantics", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "number", "password");
  const e = f.store.get(1);
  assert.equal(f.store.db.prepare("PRAGMA user_version").get().user_version, 1);
  assert.equal(
    f.store.db.prepare("PRAGMA journal_mode").get().journal_mode,
    "wal",
  );
  assert.equal(f.store.db.prepare("PRAGMA foreign_keys").get().foreign_keys, 1);
  assert.equal(f.store.db.prepare("PRAGMA synchronous").get().synchronous, 2);
  assert.equal(f.store.db.prepare("PRAGMA busy_timeout").get().timeout, 5000);
  f.advance(60000);
  f.portal.check = async () => ({ stage: "changed" });
  await f.cases.status(1);
  assert.equal(
    f.store.db.prepare("SELECT count(*) AS n FROM history").get().n,
    1,
  );
  f.restart();
  assert.equal(f.store.get(1).stage, "changed");
  await f.cases.remove(1);
  assert.throws(() => f.store.get(1), { code: "not_found" });
  const tombstone = f.store.db
    .prepare("SELECT * FROM enrollments WHERE id=?")
    .get(e.id);
  assert.equal(tombstone.owner_id, 1);
  assert.equal(tombstone.password, null);
  assert.equal(tombstone.case_number, null);
  assert.equal(tombstone.stage, null);
  assert.equal(tombstone.checked_at, null);
  assert.ok(tombstone.deleted_at);
  assert.equal(
    f.store.db.prepare("SELECT count(*) AS n FROM history").get().n,
    0,
  );
  assert.deepEqual(
    f.store.db
      .prepare("SELECT subject_type FROM cooldowns")
      .all()
      .map((r) => r.subject_type),
    ["manual_check"],
  );
});
test("history insertion failure rolls back observation, remove failure rolls back tombstone", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  const e = f.store.get(1);
  f.store.db.exec(
    "CREATE TRIGGER reject_history BEFORE INSERT ON history BEGIN SELECT RAISE(ABORT,'test'); END;",
  );
  assert.throws(() => f.store.observe(1, e.id, "new", f.now + 1));
  assert.equal(f.store.get(1).stage, "initial");
  f.store.db.exec(
    "CREATE TRIGGER reject_delete BEFORE DELETE ON cooldowns BEGIN SELECT RAISE(ABORT,'test'); END;",
  );
  f.store.reserve(1, e.id, true, f.now);
  assert.throws(() => f.store.remove(1, e.id, f.now));
  assert.equal(f.store.get(1).password, "p");
});
test("one active owner, deterministic inventory and reenrollment", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(9, "n", "p");
  await f.cases.enroll(2, "n", "p");
  assert.deepEqual(f.store.owners(), [2, 9]);
  await assert.rejects(f.cases.enroll(9, "n", "p"), { code: "exists" });
  await f.cases.remove(9);
  await f.cases.enroll(9, "other", "pass");
  assert.equal(f.store.get(9).number, "other");
});
