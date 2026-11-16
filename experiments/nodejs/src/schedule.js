import { active, fail, wait } from "./common.js";
const formatter = new Intl.DateTimeFormat("en-GB", {
  timeZone: "Europe/Warsaw",
  weekday: "short",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
export function nextSlot(after) {
  let next = Math.floor(after / 3600000) * 3600000 + 3600000;
  for (let i = 0; i < 8 * 24; i++, next += 3600000) {
    const p = Object.fromEntries(
      formatter.formatToParts(next).map((x) => [x.type, x.value]),
    );
    if (
      !["Sat", "Sun"].includes(p.weekday) &&
      [8, 10, 12, 14, 16, 18].includes(Number(p.hour)) &&
      p.minute === "00"
    )
      return next;
  }
  throw fail("scheduler");
}
export class Schedule {
  constructor(
    cases,
    { now = Date.now, waiting = wait, report = () => {} } = {},
  ) {
    Object.assign(this, { cases, now, waiting, report });
    this.running = false;
  }
  async run(signal) {
    if (this.running) throw fail("scheduler");
    this.running = true;
    try {
      while (true) {
        active(signal);
        const slot = nextSlot(this.now());
        await this.waiting(slot - this.now(), signal);
        active(signal);
        const now = this.now();
        if (now < slot || now >= slot + 60000) continue;
        let owners;
        try {
          owners = this.cases.scheduledOwners();
        } catch {
          active(signal);
          this.report("inventory_failed");
          continue;
        }
        for (const owner of owners) {
          active(signal);
          try {
            const status = await this.cases.scheduled(owner, signal);
            if (status.error) this.report("check_failed");
          } catch {
            active(signal);
            this.report("check_failed");
          }
        }
      }
    } finally {
      this.running = false;
    }
  }
}
