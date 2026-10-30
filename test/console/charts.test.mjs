// Unit tests for the console's dashboard module. Run with
// `make console-test` (node --test, no npm dependencies).
import { test } from "node:test";
import assert from "node:assert/strict";

import charts from "../../internal/gateway/console/static/charts.js";

const {
  RANGES, parseRange, CLASSES, historyPath, chartSeries, niceMax, chartGeometry, nearestIndex, moveIndex, readout, tableRows,
  alertMarks, topLists, rangeSummary, toolRows, dashboardNote, grafanaLinks,
} = charts;

const T0 = Date.UTC(2026, 9, 25, 12, 0, 0);
const MIN = 60e3;

// history is a /v1/history answer of an hour of one-minute points; fill
// returns the fields of point i.
function history(fill = () => ({}), opts = {}) {
  const step = opts.stepSeconds || 60;
  const count = opts.count || 60;
  const start = T0 - (count - 1) * step * 1000;
  const points = [];
  for (let i = 0; i < count; i++) {
    points.push({ time: new Date(start + i * step * 1000).toISOString(), ok: 0, toolError: 0, upstreamError: 0, other: 0, errorRate: null, p50Ms: null, p95Ms: null, bytesIn: 0, bytesOut: 0, ...fill(i) });
  }
  return {
    instance: "kavrynt-gateway-abc", range: "1h", stepSeconds: step, scope: {},
    start: new Date(start).toISOString(), end: new Date(T0 + step * 1000).toISOString(), since: opts.since || new Date(start).toISOString(),
    points, routes: [], tools: [], alerts: [], ...opts.extra,
  };
}

// Verifies SPEC-0024-R06: the range and scope make the request, and only
// the served ranges are accepted.
test("historyPath and parseRange", () => {
  assert.deepEqual(Object.keys(RANGES), ["1h", "6h", "24h"]);
  assert.equal(parseRange("6h"), "6h");
  assert.equal(parseRange("7d"), "1h");
  assert.equal(parseRange("__proto__"), "1h");
  assert.equal(historyPath("24h", "", ""), "/v1/history?range=24h");
  assert.equal(historyPath("6h", "team-a.search", ""), "/v1/history?range=6h&route=team-a.search");
  assert.equal(historyPath("1h", "team-a.search", "look up"), "/v1/history?range=1h&route=team-a.search&tool=look+up");
  assert.equal(historyPath("1h", "", "lookup"), "/v1/history?range=1h", "a tool needs its route");
});

// Verifies SPEC-0024-R06 and SPEC-0024-R11: requests per minute by outcome
// class, error rate, latency, and bytes per second; a tool has no bytes.
test("chartSeries", () => {
  const h = history((i) => (i === 59 ? { ok: 6, toolError: 2, upstreamError: 1, other: 1, errorRate: 0.4, p50Ms: 12, p95Ms: 80, bytesIn: 600, bytesOut: 1200 } : {}));
  const list = chartSeries(h);
  assert.deepEqual(list.map((c) => c.id), ["requests", "errors", "latency", "bytes"]);
  const requests = list[0];
  assert.equal(requests.kind, "bars");
  assert.deepEqual(requests.series.map((s) => s.key), CLASSES.map((c) => c.key));
  assert.deepEqual(requests.series.map((s) => s.values[59].v), [6, 2, 1, 1]);
  assert.equal(list[1].series[0].values[59].v, 0.4);
  assert.equal(list[1].series[0].values[0].v, null, "no calls: no error rate");
  assert.deepEqual(list[2].series.map((s) => s.values[59].v), [12, 80]);
  assert.deepEqual(list[3].series.map((s) => s.values[59].v), [10, 20], "bytes per second");

  const fiveMinutes = chartSeries(history((i) => (i === 0 ? { ok: 50 } : {}), { stepSeconds: 300, count: 12 }));
  assert.equal(fiveMinutes[0].series[0].values[0].v, 10, "50 calls in five minutes are 10 a minute");

  const tool = chartSeries(history((i) => ({ ok: i, bytesIn: undefined, bytesOut: undefined })));
  assert.deepEqual(tool.map((c) => c.id), ["requests", "errors", "latency"]);
  assert.deepEqual(chartSeries({}), chartSeries({ points: [] }).filter(Boolean));
});

// Verifies SPEC-0024-R08 and SPEC-0024-R11: geometry for a width, stacked
// bars, lines broken at gaps, the hover index, and the area before the
// replica started.
test("chartGeometry", () => {
  assert.equal(niceMax(0), 1);
  assert.equal(niceMax(7), 10);
  assert.equal(niceMax(0.3), 0.5);
  assert.equal(niceMax(120), 200);

  const h = history((i) => (i === 10 ? { ok: 3, toolError: 1, errorRate: 0.25, p50Ms: 5 } : i === 11 || i === 13 ? { ok: 1, errorRate: 0, p50Ms: 5 } : {}),
    { since: new Date(T0 - 50 * MIN).toISOString() });
  const [requests, errors, latency] = chartSeries(h);
  const opts = { width: 400, height: 160, start: Date.parse(h.start), end: Date.parse(h.end), since: Date.parse(h.since), stepSeconds: 60 };

  const bars = chartGeometry(requests, opts);
  assert.equal(bars.width, 400);
  assert.equal(bars.top, 5, "4 calls a minute round up to 5");
  assert.equal(bars.bars.length, 4, "ok and tool errors at minute 10, ok at 11 and 13");
  const [ok, toolError] = bars.bars;
  assert.equal(ok.cls, "ok");
  assert.equal(toolError.cls, "warn");
  assert.ok(Math.abs(toolError.y + toolError.h - ok.y) < 1e-9, "tool errors are stacked on ok");
  assert.ok(bars.bars.every((b) => b.x >= bars.plot.x && b.x + b.w <= bars.plot.x + bars.plot.w + 1e-9 && b.h > 0));
  assert.deepEqual(bars.yTicks.map((t) => t.label), ["0/min", "2.5/min", "5/min"]);
  assert.ok(bars.xTicks.length >= 3 && bars.xTicks.length <= 7, "ticks every 10 or 15 minutes");
  assert.ok(bars.xTicks.every((t) => t.t % (5 * MIN) === 0));
  assert.ok(bars.before && bars.before.w > 0, "the area before since is marked");
  assert.equal(bars.pointX.length, 60);

  const rate = chartGeometry(errors, opts);
  assert.equal(rate.top, 0.5, "error rate scale fits its data, at most 100 %");
  assert.equal(chartGeometry({ ...errors, series: [{ ...errors.series[0], values: errors.series[0].values.map((p) => ({ ...p, v: p.v === null ? null : 3 })) }] }, opts).top, 1);
  const line = chartGeometry(latency, opts).lines[0].d;
  assert.equal((line.match(/M/g) || []).length, 2, "the gap at minute 12 breaks the line: " + line);
  assert.match(line, /^M[\d.]+ [\d.]+ l0 0 L[\d.]+ [\d.]+ M[\d.]+ [\d.]+ l0 0$/, "isolated points are dots");

  const narrow = chartGeometry(requests, { ...opts, width: 10 });
  assert.equal(narrow.width, 120, "a chart is never narrower than 120 px");

  assert.equal(nearestIndex(bars, bars.pointX[42] + 1), 42);
  assert.equal(nearestIndex({ pointX: [] }, 10), -1);
  assert.equal(moveIndex(-1, "ArrowLeft", 60), 58, "from no cursor, keys start at the newest point");
  assert.equal(moveIndex(5, "ArrowRight", 60), 6);
  assert.equal(moveIndex(0, "ArrowLeft", 60), 0);
  assert.equal(moveIndex(5, "End", 60), 59);
  assert.equal(moveIndex(5, "Home", 60), 0);
  assert.equal(moveIndex(55, "PageDown", 60), 59);
  assert.equal(moveIndex(3, "Enter", 60), 3);
  assert.equal(moveIndex(3, "ArrowLeft", 0), -1);
});

// Verifies SPEC-0024-R08: hover and keyboard show a point's values, and a
// table holds the same numbers as the chart.
test("readout and tableRows", () => {
  const h = history((i) => (i === 59 ? { ok: 12, toolError: 3, errorRate: 0.2, p50Ms: 4, p95Ms: 1500, bytesIn: 6144, bytesOut: 60 } : {}));
  const [requests, errors, latency, bytes] = chartSeries(h);
  assert.match(readout(requests, 59), /^\d\d:\d\d · ok 12\/min, Tool errors 3\/min, Upstream failures 0\/min, Refused and other 0\/min$/);
  assert.match(readout(errors, 59), /Error rate 20 %$/);
  assert.match(readout(latency, 59), /p50 4 ms, p95 1\.50 s$/);
  assert.match(readout(bytes, 59), /In 102 B\/s, Out 1 B\/s$/);
  assert.match(readout(errors, 0), /Error rate –$/);
  assert.equal(readout(errors, 99), "");

  const rows = tableRows(latency);
  assert.equal(rows.length, 60);
  assert.deepEqual(rows[59].slice(1), ["4 ms", "1.50 s"]);
  assert.deepEqual(rows[0].slice(1), ["–", "–"]);
  assert.match(rows[59][0], /^\d\d:\d\d$/);
});

// Verifies SPEC-0024-R09: alert starts and resolutions inside the range
// are marked, oldest first; those outside are not.
test("alertMarks", () => {
  const start = T0 - 60 * MIN;
  const end = T0;
  const iso = (t) => new Date(t).toISOString();
  const alerts = [
    { name: "ToolErrors", scope: { route: "a.one", tool: "lookup" }, since: iso(T0 - 40 * MIN), resolvedAt: iso(T0 - 10 * MIN) },
    { name: "ServerNotReady", scope: { route: "a.one" }, since: iso(T0 - 3 * 60 * MIN) },
    { name: "SlowRoute", scope: { route: "a.one" }, since: iso(T0 - 2 * 60 * MIN), resolvedAt: iso(T0 - 30 * MIN) },
    { name: 42 },
    null,
  ];
  const marks = alertMarks(alerts, start, end);
  assert.deepEqual(marks.map((m) => [m.alert.name, m.kind, (end - m.t) / MIN]), [
    ["ToolErrors", "start", 40], ["SlowRoute", "resolved", 30], ["ToolErrors", "resolved", 10]]);
  assert.deepEqual(alertMarks(undefined, start, end), []);
});

// Verifies SPEC-0024-R06: the ten busiest, and the ten with the highest
// error rate among those with at least 20 calls and any error.
test("topLists", () => {
  const totals = [];
  for (let i = 0; i < 15; i++) totals.push({ route: "ns.s" + String(i).padStart(2, "0"), calls: 100 + i, errors: i, errorRate: i / (100 + i) });
  totals.push({ route: "ns.quiet", calls: 19, errors: 19, errorRate: 1 });
  totals.push({ route: "ns.idle", calls: 0, errors: 0, errorRate: null });
  totals.push({ route: "ns.tie", calls: 114, errors: 0, errorRate: 0 });
  const { busiest, failing } = topLists(totals);
  assert.equal(busiest.length, 10);
  assert.deepEqual(busiest.slice(0, 3).map((t) => t.route), ["ns.s14", "ns.tie", "ns.s13"]);
  assert.equal(failing.length, 10);
  assert.equal(failing[0].route, "ns.s14");
  assert.ok(!failing.some((t) => t.route === "ns.quiet"), "fewer than 20 calls are not ranked by error rate");
  assert.ok(!failing.some((t) => t.errorRate === 0), "no errors, not failing");
  assert.ok(!busiest.some((t) => t.route === "ns.idle"));
  assert.deepEqual(topLists(null), { busiest: [], failing: [] });

  const tools = topLists([{ route: "a", tool: "x", calls: 30, errorRate: 0.5 }, { route: "b", tool: "x", calls: 30, errorRate: 0.5 }]);
  assert.deepEqual(tools.failing.map((t) => t.route), ["a", "b"], "ties are ordered by route and tool");
});

// Verifies SPEC-0024-R07: the per-tool table lists discovered tools and
// tools with calls, busiest first.
test("toolRows and rangeSummary", () => {
  const rows = toolRows([{ route: "a.one", tool: "lookup", calls: 30, errors: 6, errorRate: 0.2, p95Ms: 40 }, { route: "a.one", tool: "other", calls: 2, errors: 0, errorRate: 0, p95Ms: 1 }],
    ["fetch", "lookup"]);
  assert.deepEqual(rows.map((r) => [r.tool, r.calls]), [["lookup", 30], ["other", 2], ["fetch", 0]]);
  assert.equal(rows[2].errorRate, null);

  const summary = rangeSummary(history((i) => (i < 2 ? { ok: 3, toolError: 1 } : {})));
  assert.deepEqual(summary, { calls: 8, errorRate: 0.25, ok: 6, toolError: 2, upstreamError: 0, other: 0 });
  assert.equal(rangeSummary({}).errorRate, null);
});

// Verifies SPEC-0024-R10: the page says whose numbers these are and since
// when, and links Grafana only from an http or https base.
test("dashboardNote and grafanaLinks", () => {
  const started = history(() => ({}), { since: new Date(T0 - 2 * 60 * MIN - 5 * MIN).toISOString() });
  const note = dashboardNote({ ...started, range: "24h", start: new Date(T0 - 24 * 60 * MIN).toISOString() }, T0);
  assert.match(note, /^Gateway replica kavrynt-gateway-abc only, since it started 2 h 5 min ago \(\d\d:\d\d\) · kept in memory, lost on restart$/);
  assert.equal(dashboardNote(history(), T0), "Gateway replica kavrynt-gateway-abc only · kept in memory for up to 24 hours, lost on restart");
  assert.match(dashboardNote(null), /^This Gateway replica only/);

  assert.equal(grafanaLinks("", "a.one"), null);
  assert.equal(grafanaLinks("javascript:alert(1)//", "a.one"), null);
  assert.equal(grafanaLinks("https://grafana.example.com/?x=1", ""), null);
  assert.deepEqual(grafanaLinks("https://grafana.example.com/", ""), [{ title: "Kavrynt MCP Traffic", href: "https://grafana.example.com/d/kavrynt-mcp-traffic" }]);
  assert.deepEqual(grafanaLinks("http://grafana.svc:3000/grafana", "team-a.search.v2"), [
    { title: "Kavrynt MCP Traffic", href: "http://grafana.svc:3000/grafana/d/kavrynt-mcp-traffic" },
    { title: "Kavrynt MCP Server", href: "http://grafana.svc:3000/grafana/d/kavrynt-mcp-server?var-namespace=team-a&var-name=search.v2" },
  ]);
});
