// Unit tests for the console's pure statistics module. Run with
// `make console-test` (node --test, no npm dependencies).
import { test } from "node:test";
import assert from "node:assert/strict";

import stats from "../../internal/gateway/console/static/stats.js";

// The console scripts are classic scripts; under Node they export through
// module.exports.
const {
  SCOPE_NOTE, OUTCOMES, overview, attention, failingTools, toolSummary, filterTraffic, serverHealth,
  filterServers, catalog, searchCatalog, diagnostics, stripQuery,
} = stats;

let seq = 0;
function rec(route, method, tool, outcome, durationMs = 10) {
  seq++;
  return { id: seq, time: new Date(Date.UTC(2026, 9, 23, 12, 0, seq)).toISOString(), route, method, tool, outcome, status: outcome === "upstream_unreachable" ? 502 : 200, durationMs, bytesIn: 10, bytesOut: 20 };
}

function cond(type, status, reason, message = "") {
  return { type, status, reason, message, lastTransitionTime: "2026-10-23T12:00:00Z" };
}

function server(namespace, name, opts = {}) {
  return {
    namespace, name, route: namespace + "." + name, version: opts.version || "1.0.0", transport: opts.transport || "http",
    endpoint: opts.endpoint ?? "http://" + name + "." + namespace + ".svc:8080/mcp", routed: opts.routed ?? true,
    description: opts.description || "",
    conditions: opts.conditions || [cond("Accepted", "True", "Valid"), cond("Ready", "True", "Routable"), cond("ToolsDiscovered", "True", "Discovered")],
    tools: opts.tools || [], createdAt: "2026-10-23T11:00:00Z",
  };
}

const incidents = server("team-sre", "incidents", { tools: [{ name: "list_incidents", description: "List on-call incidents" }, { name: "get_incident", description: "Get one incident" }] });
const runbooks = server("team-platform", "runbooks", { tools: [{ name: "search_runbooks", description: "Full-text search over runbooks" }] });
const billing = server("team-payments", "billing", {
  conditions: [cond("Accepted", "True", "Valid"), cond("Ready", "True", "Routable"), cond("ToolsDiscovered", "False", "DiscoveryFailed", "initialize: dial tcp: no such host")],
});
const stdio = server("team-platform", "local-files", {
  transport: "stdio", endpoint: "", routed: false,
  conditions: [cond("Accepted", "True", "Valid"), cond("Ready", "False", "UnsupportedTransport", "Gateway routes only the http transport")],
  tools: [{ name: "read_file", description: "Read a file" }],
});
const servers = [incidents, runbooks, billing, stdio];

// Newest first, as /v1/traffic returns them.
const traffic = [
  ...Array.from({ length: 6 }, () => rec("team-payments.billing", "tools/call", "create_refund", "upstream_unreachable", 3)),
  ...Array.from({ length: 8 }, (_, i) => rec("team-sre.incidents", "tools/call", "get_incident", i < 2 ? "tool_error" : "ok", 10 + i)),
  ...Array.from({ length: 4 }, () => rec("team-sre.incidents", "tools/list", "none", "ok", 2)),
  ...Array.from({ length: 3 }, () => rec("team-platform.runbooks", "resources/list", "none", "rpc_error", 1)),
  ...Array.from({ length: 5 }, () => rec("team-platform.runbooks", "tools/call", "search_runbooks", "ok", 5)),
].reverse();

// Verifies SPEC-0002-R01.
test("overview counts servers, routed servers, tools, requests, error rate, and p95", () => {
  const o = overview(servers, traffic);
  assert.equal(o.servers, 4);
  assert.equal(o.namespaces, 3);
  assert.equal(o.routed, 3);
  assert.equal(o.tools, 4);
  assert.equal(o.requests, 26);
  assert.equal(o.errorRate, 11 / 26);
  assert.equal(typeof o.p95, "number");
  assert.equal(overview([], []).errorRate, null);
});

// Verifies SPEC-0002-R02.
test("attention lists every server that is not routed or whose tool discovery failed, with reason and message", () => {
  const items = attention(servers, []);
  assert.deepEqual(items.map((i) => i.server.route).sort(), ["team-payments.billing", "team-platform.local-files"]);
  const billingItem = items.find((i) => i.server.route === "team-payments.billing");
  assert.match(billingItem.health.detail, /Tool discovery failed: initialize: dial tcp: no such host/);
  const stdioItem = items.find((i) => i.server.route === "team-platform.local-files");
  assert.equal(stdioItem.health.label, "Not proxied (stdio)");
  assert.equal(stdioItem.health.detail, "Gateway routes only the http transport");
  assert.deepEqual(attention([incidents, runbooks], traffic), []);
});

// Verifies SPEC-0002-R03.
test("failing tools are the five tools with the most failures, and tools without failures are never listed", () => {
  const rows = failingTools(traffic);
  assert.deepEqual(rows.map((r) => [r.route, r.name, r.errors, r.calls]), [
    ["team-payments.billing", "create_refund", 6, 6],
    ["team-sre.incidents", "get_incident", 2, 8],
  ]);
  assert.equal(rows[1].rate, 0.25);
  assert.ok(!rows.some((r) => r.name === "search_runbooks"), "tool without errors listed");
  assert.ok(!rows.some((r) => r.name === "resources/list"), "methods are not tools");

  const many = [];
  for (let t = 0; t < 7; t++) for (let i = 0; i <= t; i++) many.push(rec("ns.s", "tools/call", "tool" + t, "tool_error"));
  assert.deepEqual(failingTools(many).map((r) => r.name), ["tool6", "tool5", "tool4", "tool3", "tool2"]);
});

// Verifies SPEC-0002-R04.
test("the catalog lists every discovered tool of every server and searches name, description, and route", () => {
  const entries = catalog(servers);
  assert.deepEqual(entries.map((e) => e.tool + "@" + e.route), [
    "get_incident@team-sre.incidents", "list_incidents@team-sre.incidents", "read_file@team-platform.local-files", "search_runbooks@team-platform.runbooks",
  ]);
  assert.equal(entries[0].routed, true);
  assert.deepEqual(searchCatalog(entries, "SEARCH").map((e) => e.tool), ["search_runbooks"]);
  assert.deepEqual(searchCatalog(entries, "on-call").map((e) => e.tool), ["list_incidents"]);
  assert.deepEqual(searchCatalog(entries, "team-platform").map((e) => e.tool), ["read_file", "search_runbooks"]);
  assert.equal(searchCatalog(entries, "").length, 4);
});

// Verifies SPEC-0002-R05.
test("a tool can be tried only through a routed server, and otherwise says why", () => {
  const entries = catalog(servers);
  const routed = entries.find((e) => e.tool === "get_incident");
  assert.equal(routed.playable, true);
  assert.equal(routed.reason, "");
  const unrouted = entries.find((e) => e.tool === "read_file");
  assert.equal(unrouted.playable, false);
  assert.match(unrouted.reason, /Not routed: Not proxied \(stdio\)/);
});

// Verifies SPEC-0002-R06.
test("traffic filters by route, method, tool, and individual outcome", () => {
  assert.equal(filterTraffic(traffic, { route: "team-platform.runbooks" }).length, 8);
  assert.equal(filterTraffic(traffic, { method: "tools/list" }).length, 4);
  assert.equal(filterTraffic(traffic, { tool: "get_incident" }).length, 8);
  assert.equal(filterTraffic(traffic, { outcome: "tool_error" }).length, 2);
  assert.equal(filterTraffic(traffic, { outcome: "rpc_error" }).length, 3);
  assert.equal(filterTraffic(traffic, { outcome: "errors" }).length, 11);
  assert.equal(filterTraffic(traffic, { route: "team-sre.incidents", tool: "get_incident", outcome: "ok" }).length, 6);
  assert.equal(filterTraffic(traffic, {}).length, traffic.length);
});

// Verifies SPEC-0002-R07.
test("the per-tool summary gives calls, error rate, p50, and p95 per route and tool", () => {
  const rows = toolSummary(traffic.filter((r) => r.route === "team-sre.incidents"));
  const get = rows.find((r) => r.name === "get_incident");
  assert.equal(get.calls, 8);
  assert.equal(get.errors, 2);
  assert.equal(get.rate, 0.25);
  assert.equal(get.p50, 14);
  assert.equal(get.p95, 17);
  assert.equal(get.isTool, true);
  const list = rows.find((r) => r.name === "tools/list");
  assert.equal(list.isTool, false, "methods without a tool are summarized under the method");
  assert.equal(list.rate, 0);
  assert.equal(rows[0].name, "get_incident", "busiest first");
});

// Verifies SPEC-0002-R08.
test("every outcome has a plain-language definition", () => {
  for (const [name, outcome] of Object.entries(OUTCOMES)) {
    assert.ok(outcome.definition.length > 20, name + " has no definition");
    assert.ok(["ok", "warn", "bad"].includes(outcome.cls), name);
  }
});

// The wording of the replica scope note. That every traffic view shows it
// needs a browser test, which is not set up yet.
test("the traffic scope note names the replica and the 500-request window", () => {
  assert.match(SCOPE_NOTE, /This Gateway replica only/);
  assert.match(SCOPE_NOTE, /last 500 requests/);
});

// Verifies SPEC-0002-R11.
test("diagnostics carry route, transport, conditions, tools, and traffic metadata, but no query string, inputs, or results", () => {
  const secret = server("team-sre", "secretive", {
    endpoint: "https://mcp.example.com/mcp?token=secret-token#frag",
    tools: [{ name: "lookup", description: "x", inputSchema: { properties: { q: { type: "string" } } } }],
    // The Operator quotes the full endpoint URL in condition messages.
    conditions: [cond("Accepted", "True", "Valid"), cond("Ready", "True", "Routable"),
      cond("ToolsDiscovered", "False", "DiscoveryFailed", 'initialize: Post "https://mcp.example.com/mcp?token=secret-token#frag": timeout')],
  });
  const records = [rec("team-sre.secretive", "tools/call", "lookup", "ok"), rec("other.route", "tools/call", "other_tool", "ok")];
  const text = diagnostics(secret, records, "0.0.6-beta.1", new Date("2026-10-23T12:00:00Z"));
  assert.match(text, /Console version: 0\.0\.6-beta\.1/);
  assert.match(text, /Route: \/mcp\/team-sre\.secretive/);
  assert.match(text, /Transport: http/);
  assert.match(text, /Endpoint: https:\/\/mcp\.example\.com\/mcp\n/);
  assert.match(text, /Ready=True reason=Routable/);
  assert.match(text, /ToolsDiscovered=False reason=DiscoveryFailed .*mcp\.example\.com\/mcp\?\[redacted\]/);
  assert.match(text, /Tools \(1\): lookup/);
  assert.match(text, /requests=1 errors=0/);
  assert.doesNotMatch(text, /secret-token|token=|#frag/);
  assert.doesNotMatch(text, /other_tool|other\.route/, "other routes' traffic leaks into diagnostics");
  assert.doesNotMatch(text, /inputSchema|properties/);
  assert.equal(stripQuery("http://a/b?c=d#e"), "http://a/b");
});

// Verifies SPEC-0002-R19.
test("a routed server is not shown as healthy when discovery failed or most recent requests failed", () => {
  const failing = server("team-payments", "billing");
  const health = serverHealth(failing, traffic);
  assert.equal(health.label, "Needs attention");
  assert.equal(health.cls, "bad");
  assert.equal(health.attention, true);
  assert.match(health.detail, /6 of its last 6 requests failed, mostly upstream_unreachable/);
  assert.equal(health.stats.calls, 6);
  assert.equal(health.stats.rate, 1);

  const discovery = serverHealth(billing, []);
  assert.equal(discovery.label, "Needs attention");
  assert.equal(discovery.cls, "warn");

  // Below the request floor or under half failing: healthy, with its numbers.
  const few = traffic.filter((r) => r.route === "team-payments.billing").slice(0, 4);
  assert.equal(serverHealth(failing, few).label, "Routed");
  const fine = serverHealth(incidents, traffic);
  assert.equal(fine.label, "Routed");
  assert.equal(fine.attention, false);
  assert.equal(fine.stats.calls, 12);
  assert.equal(fine.stats.errors, 2);

  assert.deepEqual(filterServers(servers, traffic, { state: "attention" }).map((s) => s.route), ["team-payments.billing", "team-platform.local-files"]);
});

// Verifies SPEC-0004-R04.
test("declared and reported versions are compared, and a difference does not change health", () => {
  const { versionInfo } = stats;
  const same = { ...incidents, version: "0.2.0", serverInfo: { name: "incidents-mcp", version: "0.2.0" } };
  const differs = { ...incidents, version: "0.1.0", serverInfo: { name: "incidents-mcp", version: "0.2.0" } };
  assert.deepEqual(versionInfo(same), { declared: "0.2.0", reported: "0.2.0", reportedName: "incidents-mcp", differs: false });
  assert.deepEqual(versionInfo(differs), { declared: "0.1.0", reported: "0.2.0", reportedName: "incidents-mcp", differs: true });
  assert.equal(versionInfo(incidents).reported, "", "no serverInfo recorded yet");
  assert.equal(versionInfo(incidents).differs, false);
  assert.equal(versionInfo({ ...incidents, serverInfo: { name: "only-a-name" } }).differs, false);
  assert.equal(serverHealth(differs, []).label, "Routed", "a version difference must not mark the server as needing attention");
  assert.match(diagnostics(differs, [], "x"), /Reported: incidents-mcp 0\.2\.0/);
});
