// Unit tests for the console's URL, host, and polling state. Run with
// `make console-test` (node --test, no npm dependencies).
import { test } from "node:test";
import assert from "node:assert/strict";

import state from "../../internal/gateway/console/static/state.js";

// The console scripts are classic scripts; under Node they export through
// module.exports.
const { parseHash, formatHash, isLoopbackHost, createPoller } = state;

// Verifies SPEC-0002-R10.
test("filters round-trip through the URL hash", () => {
  const filters = { route: "team-sre.incidents", method: "tools/call", tool: "get_incident", outcome: "upstream_unreachable" };
  const hash = formatHash("traffic", [], filters);
  assert.equal(hash, "#/traffic?route=team-sre.incidents&method=tools%2Fcall&tool=get_incident&outcome=upstream_unreachable");
  assert.deepEqual(parseHash(hash), { page: "traffic", rest: [], params: filters });

  assert.equal(formatHash("servers", [], { q: "", ns: "team-sre", state: "" }), "#/servers?ns=team-sre");
  assert.deepEqual(parseHash(formatHash("tools", [], { q: "search & find" })).params, { q: "search & find" });

  const detail = parseHash(formatHash("servers", ["team-sre.incidents", "traffic"]));
  assert.deepEqual(detail, { page: "servers", rest: ["team-sre.incidents", "traffic"], params: {} });

  // v1 links keep working.
  assert.deepEqual(parseHash("#/playground/team-sre.incidents/get_incident"), { page: "playground", rest: ["team-sre.incidents", "get_incident"], params: {} });
  assert.equal(parseHash("").page, "");
  assert.equal(parseHash("#/traffic?bogus=%E0%A4%A").page, "traffic");
});

// Verifies SPEC-0002-R12.
test("only loopback hosts skip the no-login notice", () => {
  for (const host of ["localhost", "LOCALHOST", "127.0.0.1", "127.1.2.3", "::1", "[::1]"]) {
    assert.equal(isLoopbackHost(host), true, host);
  }
  for (const host of ["kavrynt.example.com", "10.0.0.5", "192.168.1.10", "localhost.example.com", "127.0.0.1.nip.io", "::ffff:10.0.0.1", ""]) {
    assert.equal(isLoopbackHost(host), false, host);
  }
});

function fakeDocument() {
  const listeners = new Set();
  return {
    hidden: false,
    addEventListener: (type, fn) => listeners.add(fn),
    removeEventListener: (type, fn) => listeners.delete(fn),
    setHidden(hidden) { this.hidden = hidden; for (const fn of listeners) fn(); },
  };
}

function fakeTimers() {
  const live = new Map();
  let next = 0;
  return {
    live,
    setInterval: (fn, ms) => { live.set(++next, { fn, ms }); return next; },
    clearInterval: (id) => { live.delete(id); },
    tick() { for (const { fn } of [...live.values()]) fn(); },
  };
}

// Verifies SPEC-0002-R14.
test("polling stops while the page is hidden and refreshes once when it is visible again", () => {
  const doc = fakeDocument();
  const timers = fakeTimers();
  const calls = { servers: 0, traffic: 0 };
  const poller = createPoller(doc, [
    { fn: () => calls.servers++, ms: 5000 },
    { fn: () => calls.traffic++, ms: 2000 },
  ], timers);

  poller.start();
  assert.deepEqual(calls, { servers: 1, traffic: 1 }, "runs every task at start");
  assert.equal(timers.live.size, 2);
  timers.tick();
  assert.deepEqual(calls, { servers: 2, traffic: 2 });

  doc.setHidden(true);
  assert.equal(timers.live.size, 0, "timers cleared while hidden");
  assert.equal(poller.running, false);
  timers.tick();
  assert.deepEqual(calls, { servers: 2, traffic: 2 }, "no polling while hidden");

  doc.setHidden(false);
  assert.deepEqual(calls, { servers: 3, traffic: 3 }, "one refresh when visible again");
  assert.equal(timers.live.size, 2);

  poller.stop();
  assert.equal(timers.live.size, 0);
});

test("a poller started in a hidden page waits until it is visible", () => {
  const doc = fakeDocument();
  doc.hidden = true;
  const timers = fakeTimers();
  let n = 0;
  createPoller(doc, [{ fn: () => n++, ms: 1000 }], timers).start();
  assert.equal(n, 0);
  doc.setHidden(false);
  assert.equal(n, 1);
});

test("session responses map to login off, signed in, or login required, and fail closed", () => {
  const { sessionState } = state;
  assert.deepEqual(sessionState(200, { enabled: false }), { enabled: false, signedIn: true, username: "", expiresAt: "" });
  assert.deepEqual(sessionState(200, { enabled: true, username: "admin", expiresAt: "2026-10-23T13:00:00Z" }),
    { enabled: true, signedIn: true, username: "admin", expiresAt: "2026-10-23T13:00:00Z" });
  assert.deepEqual(sessionState(401, { error: "login required", enabled: true }), { enabled: true, signedIn: false, username: "", expiresAt: "" });
  // Anything unexpected is not a session; the console then shows the login form.
  for (const [status, body] of [[200, {}], [200, null], [500, null], [403, { error: "origin not allowed" }]]) {
    assert.equal(sessionState(status, body), null, status + " " + JSON.stringify(body));
  }
});

test("login failures are explained without revealing which field was wrong", () => {
  const { loginMessage } = state;
  assert.equal(loginMessage(401), "Wrong username or password.");
  assert.equal(loginMessage(429, "42"), "Too many failed attempts. Try again in 42 s.");
  assert.equal(loginMessage(429, null), "Too many failed attempts. Try again in a minute.");
  assert.match(loginMessage(403), /--allowed-origins/);
  assert.match(loginMessage(503), /not configured/);
  assert.match(loginMessage(0), /did not answer/);
  assert.equal(loginMessage(500), "Login failed (HTTP 500).");
});
