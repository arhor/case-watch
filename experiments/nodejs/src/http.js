import http from "node:http";
import https from "node:https";
import { active, bounded, fail } from "./common.js";

// A single exchange: never follows redirects or retries a stale pooled socket.
export class Http {
  constructor() {
    this.agents = {
      "http:": new http.Agent({ keepAlive: true }),
      "https:": new https.Agent({ keepAlive: true }),
    };
  }
  async request(
    url,
    {
      method = "GET",
      body,
      headers = {},
      signal,
      timeout = 10000,
      limit = 4 << 20,
    } = {},
  ) {
    active(signal);
    const bound = bounded(signal, timeout);
    try {
      return await new Promise((resolve, reject) => {
        const target = new URL(url);
        const protocol = target.protocol === "https:" ? https : http;
        const req = protocol.request(
          target,
          {
            method,
            headers,
            agent: this.agents[target.protocol],
            signal: bound.signal,
          },
          (res) => {
            const chunks = [];
            let length = 0;
            res.on("data", (chunk) => {
              length += chunk.length;
              if (length > limit) {
                res.destroy(fail("protocol"));
                return;
              }
              chunks.push(chunk);
            });
            res.on("error", reject);
            res.on("end", () =>
              resolve({
                status: res.statusCode,
                headers: res.headers,
                body: Buffer.concat(chunks).toString("utf8"),
              }),
            );
          },
        );
        req.on("error", reject);
        req.end(body);
      });
    } catch (error) {
      if (bound.signal.aborted) throw bound.signal.reason;
      throw fail(error.code === "protocol" ? "protocol" : "transport");
    } finally {
      bound.close();
    }
  }
  close() {
    Object.values(this.agents).forEach((agent) => agent.destroy());
  }
}

// Redirects are prohibited, so cookies are used only on the configured origin.
export class Cookies {
  constructor() {
    this.items = new Map();
  }
  set(url, headers, now = Date.now()) {
    const u = new URL(url);
    for (const header of headers ?? []) {
      const [pair, ...attributes] = header.split(";");
      const eq = pair.indexOf("=");
      if (eq <= 0) continue;
      const cookie = {
        name: pair.slice(0, eq).trim(),
        value: pair.slice(eq + 1).trim(),
        domain: u.hostname,
        hostOnly: true,
        path: u.pathname.slice(0, u.pathname.lastIndexOf("/")) || "/",
        secure: false,
        expires: Infinity,
      };
      let maxAge;
      for (const attribute of attributes) {
        const [key, ...parts] = attribute.trim().split("=");
        const value = parts.join("=");
        switch (key.toLowerCase()) {
          case "domain":
            cookie.domain = value.toLowerCase().replace(/^\./, "");
            cookie.hostOnly = false;
            break;
          case "path":
            if (value.startsWith("/")) cookie.path = value;
            break;
          case "secure":
            cookie.secure = true;
            break;
          case "expires": {
            const expiry = Date.parse(value);
            if (Number.isFinite(expiry)) cookie.expires = expiry;
            break;
          }
          case "max-age":
            if (/^-?\d+$/.test(value)) maxAge = Number(value);
            break;
        }
      }
      if (maxAge !== undefined) cookie.expires = now + maxAge * 1000;
      if (
        cookie.domain !== u.hostname &&
        !u.hostname.endsWith("." + cookie.domain)
      )
        continue;
      this.items.set(`${cookie.domain}|${cookie.path}|${cookie.name}`, cookie);
    }
  }
  get(url, now = Date.now()) {
    const u = new URL(url);
    const selected = [];
    for (const [key, c] of this.items) {
      if (c.expires <= now) {
        this.items.delete(key);
        continue;
      }
      if (
        (c.hostOnly
          ? u.hostname === c.domain
          : u.hostname === c.domain || u.hostname.endsWith("." + c.domain)) &&
        (!c.secure || u.protocol === "https:") &&
        (u.pathname === c.path ||
          (u.pathname.startsWith(c.path) &&
            (c.path.endsWith("/") || u.pathname[c.path.length] === "/")))
      )
        selected.push(c);
    }
    return selected
      .sort((a, b) => b.path.length - a.path.length)
      .map((c) => `${c.name}=${c.value}`)
      .join("; ");
  }
}
