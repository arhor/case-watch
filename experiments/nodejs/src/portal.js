import { Http, Cookies } from "./http.js";
import { active, bounded, fail, wait } from "./common.js";
const integer = (n) => Number.isSafeInteger(n) && n >= 0;
export function bootstrapObject(text, marker) {
  const offset = text.indexOf(marker);
  if (offset < 0) throw fail("protocol");
  const start = offset + marker.length;
  let depth = 0,
    quoted = false,
    escaped = false,
    begin = -1;
  for (let i = start; i < text.length; i++) {
    const c = text[i];
    if (begin < 0) {
      if (/\s/.test(c)) continue;
      if (c !== "{") throw fail("protocol");
      begin = i;
    }
    if (quoted) {
      if (escaped) escaped = false;
      else if (c === "\\") escaped = true;
      else if (c === '"') quoted = false;
    } else if (c === '"') quoted = true;
    else if (c === "{") depth++;
    else if (c === "}" && --depth === 0)
      return JSON.parse(text.slice(begin, i + 1));
  }
  throw fail("protocol");
}
export class RpcState {
  constructor() {
    this.props = new Map();
    this.children = new Map();
    this.token = "";
  }
  apply(v) {
    if (!v || !integer(v.syncId) || !integer(v.clientId))
      throw fail("protocol");
    this.sync = v.syncId;
    this.client = v.clientId;
    if (typeof v["Vaadin-Security-Key"] === "string")
      this.token = v["Vaadin-Security-Key"];
    if (!Object.hasOwn(v, "changes")) return;
    if (!Array.isArray(v.changes)) throw fail("protocol");
    for (const c of v.changes) {
      if (!c || !integer(c.node)) throw fail("protocol");
      const n = c.node;
      if (!this.props.has(n)) this.props.set(n, {});
      const p = this.props.get(n),
        semantic = [0, 1, 7].includes(c.feat ?? 0);
      switch (c.type) {
        case "attach":
          break;
        case "put":
          if (typeof c.key !== "string") throw fail("protocol");
          if (semantic) p[c.key] = c.value;
          break;
        case "remove":
          if (semantic) delete p[c.key];
          break;
        case "detach":
          this.props.delete(n);
          this.children.delete(n);
          break;
        case "clear":
        case "empty":
          if (c.feat === 2) this.children.delete(n);
          break;
        case "splice": {
          if (c.feat !== 2) break;
          const old = this.children.get(n) ?? [],
            remove = c.remove ?? 0,
            added = c.addNodes ?? [];
          if (
            !integer(c.index) ||
            !integer(remove) ||
            c.index + remove > old.length ||
            !Array.isArray(added) ||
            !added.every(integer)
          )
            throw fail("protocol");
          this.children.set(n, [
            ...old.slice(0, c.index),
            ...added,
            ...old.slice(c.index + remove),
          ]);
          break;
        }
        default:
          throw fail("protocol");
      }
    }
  }
  response(text) {
    const values = JSON.parse(text.replace(/^for\(;;\);/, ""));
    if (!Array.isArray(values) || !values.length) throw fail("protocol");
    for (const v of values) this.apply(v);
  }
  find(tag, label = "") {
    const found = [...this.props].filter(
      ([n, p]) =>
        p.tag === tag &&
        (!label || p.label === label) &&
        (tag !== "vaadin-button" ||
          (this.children.get(n) ?? []).some(
            (child) => this.props.get(child)?.text === "Log in",
          )),
    );
    if (found.length !== 1) throw fail("protocol");
    return found[0][0];
  }
  login() {
    return [
      this.find("vaadin-text-field", "Number of application"),
      this.find("vaadin-password-field", "Password"),
      this.find("vaadin-button"),
    ];
  }
  walk(n, fn, seen = new Set()) {
    if (seen.has(n)) return;
    seen.add(n);
    fn(this.props.get(n) ?? {});
    for (const child of this.children.get(n) ?? []) this.walk(child, fn, seen);
  }
  stage() {
    for (const n of this.children.keys()) {
      let caption = false;
      const values = [];
      this.walk(n, (p) => {
        if (
          p.text === "Stage of your case" ||
          p.nodeValue === "Stage of your case"
        )
          caption = true;
        if (
          p.tag === "vaadin-text-field" &&
          typeof p.value === "string" &&
          p.value.trim()
        )
          values.push(p.value);
      });
      if (caption && values.length === 1) return values[0];
    }
    try {
      this.find("vaadin-password-field", "Password");
    } catch {
      throw fail("protocol");
    }
    throw fail("authentication");
  }
}
export function event(node, name) {
  const data = {};
  if (name === "click") {
    for (const key of ["shiftKey", "metaKey", "ctrlKey", "altKey"])
      data["event." + key] = false;
    for (const key of [
      "detail",
      "clientX",
      "clientY",
      "button",
      "screenX",
      "screenY",
    ])
      data["event." + key] = 0;
  }
  return { type: "event", node, event: name, data };
}
const change = (node, value, name) => [
  { type: "mSync", node, feature: 1, property: "value", value },
  event(node, name),
];
export class Portal {
  constructor({
    base = "https://klient.gdansk.uw.gov.pl",
    http,
    now = Date.now,
    waiting = wait,
    interval = 2000,
    overall = 60000,
    perRequest = 10000,
    budget = 6,
  } = {}) {
    Object.assign(this, {
      base: base.replace(/\/$/, ""),
      http: http ?? new Http(),
      now,
      waiting,
      interval,
      overall,
      perRequest,
      budget,
    });
    this.owned = !http;
    this.sessions = new Map();
  }
  forget(owner) {
    this.sessions.delete(owner);
  }
  close() {
    this.sessions.clear();
    if (this.owned) this.http.close();
  }
  async check(owner, number, password, signal) {
    const bound = bounded(signal, this.overall),
      state = new RpcState();
    const jar = this.sessions.get(owner) ?? new Cookies();
    this.sessions.set(owner, jar);
    let last,
      requests = 0;
    const request = async (method, path, body) => {
      active(bound.signal);
      if (requests >= this.budget) throw fail("protocol");
      if (last !== undefined)
        await this.waiting(last + this.interval - this.now(), bound.signal);
      active(bound.signal);
      last = this.now();
      requests++;
      const url = this.base + path;
      const response = await this.http.request(url, {
        method,
        body: body === undefined ? undefined : JSON.stringify(body) + "\n",
        headers: {
          "Content-Type": "application/json; charset=UTF-8",
          ...(jar.get(url, this.now())
            ? { Cookie: jar.get(url, this.now()) }
            : {}),
        },
        signal: bound.signal,
        timeout: this.perRequest,
      });
      jar.set(url, response.headers["set-cookie"], this.now());
      if ([401, 403].includes(response.status)) throw fail("authentication");
      if (response.status !== 200) throw fail("transport");
      return response.body;
    };
    try {
      const html = await request("GET", "/");
      const config = bootstrapObject(html, "var config =");
      if (!integer(config["v-uiId"])) throw fail("protocol");
      state.apply(bootstrapObject(html, "var uidl ="));
      let stage;
      try {
        stage = state.stage();
      } catch {
        /* login required */
      }
      if (stage === undefined) {
        state.login();
        if (!state.token) throw fail("protocol");
        for (let step = 0; step < 4; step++) {
          const [num, pass, button] = state.login();
          const rpc = [
            [event(pass, "value-changed")],
            change(num, number, "change"),
            change(pass, password, "value-changed"),
            [event(button, "click")],
          ][step];
          state.response(
            await request("POST", `/?v-r=uidl&v-uiId=${config["v-uiId"]}`, {
              csrfToken: state.token,
              syncId: state.sync,
              clientId: state.client,
              rpc,
            }),
          );
        }
        stage = state.stage();
      }
      active(bound.signal);
      return { stage, checkedAt: this.now() };
    } catch (error) {
      this.forget(owner);
      if (bound.signal.aborted) throw bound.signal.reason;
      throw fail(
        ["authentication", "transport", "deadline", "protocol"].includes(
          error.code,
        )
          ? error.code
          : "protocol",
      );
    } finally {
      bound.close();
    }
  }
}
