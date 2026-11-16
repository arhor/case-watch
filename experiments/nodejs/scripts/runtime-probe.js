import assert from "node:assert/strict";
import { DatabaseSync } from "node:sqlite";
import { X509Certificate } from "node:crypto";
import { getCACertificates } from "node:tls";

const db = new DatabaseSync(":memory:");
db.exec("CREATE TABLE probe(value TEXT); INSERT INTO probe VALUES ('ok')");
assert.equal(db.prepare("SELECT value FROM probe").get().value, "ok");
db.close();
const time = new Intl.DateTimeFormat("en-GB", {
  timeZone: "Europe/Warsaw",
  hour: "2-digit",
  hourCycle: "h23",
});
assert.equal(time.format(Date.parse("2026-01-01T12:00:00Z")), "13");
assert.equal(time.format(Date.parse("2026-07-01T12:00:00Z")), "14");
assert.ok(getCACertificates("bundled").length > 0);
assert.ok(
  getCACertificates("extra").some(
    (pem) =>
      new X509Certificate(pem).fingerprint256 ===
      "83:C0:A5:A7:68:44:C8:40:DF:AF:82:0F:FD:02:AD:F6:57:3A:26:82:3E:F6:AF:75:8A:33:84:A0:AC:04:40:83",
  ),
);
console.log("SQLite, Warsaw DST, bundled roots and portal certificate: passed");
