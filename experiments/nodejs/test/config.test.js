import test from "node:test";
import assert from "node:assert/strict";
import { loadConfig } from "../src/config.js";
const valid = {
  CASE_WATCH_TELEGRAM_BOT_TOKEN: "123:" + "a".repeat(35),
  CASE_WATCH_DB_PATH: " ./independent.db ",
};
test("configuration preserves literal paths and rejects malformed input without exposing values", () => {
  assert.deepEqual(loadConfig(valid), {
    token: valid.CASE_WATCH_TELEGRAM_BOT_TOKEN,
    dbPath: valid.CASE_WATCH_DB_PATH,
  });
  for (const token of [
    "",
    undefined,
    "0:" + "a".repeat(35),
    "secret",
    "123:" + "a".repeat(35) + "\n",
    " 123:" + "a".repeat(35),
    "123:" + "a".repeat(36),
  ])
    assert.throws(
      () => loadConfig({ ...valid, CASE_WATCH_TELEGRAM_BOT_TOKEN: token }),
      /CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax/,
    );
  for (const dbPath of ["", undefined, "\nsecret", "a\0b", "   "])
    assert.throws(
      () => loadConfig({ ...valid, CASE_WATCH_DB_PATH: dbPath }),
      /CASE_WATCH_DB_PATH: invalid or missing path/,
    );
});
