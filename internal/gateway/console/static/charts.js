// Everything the console dashboards (SPEC-0024) compute from /v1/history:
// the charts' series, their geometry for a width, alert marks, the top
// lists, and the replica note. Pure functions with no DOM access, so they
// run under Node's test runner (test/console) as well as in the browser.
//
// Route and tool names are untrusted. Nothing here renders them; views
// insert them as text and SVG text nodes only.
"use strict";

// Shared by the browser (Kavrynt.charts) and Node's test runner (module.exports).
((exports, format) => {
  const { fmtBytes, fmtMs, fmtPct } = format;

  // RANGES are the spans /v1/history serves.
  const RANGES = {
    "1h": { label: "Last hour", ms: 3600e3 },
    "6h": { label: "Last 6 hours", ms: 6 * 3600e3 },
    "24h": { label: "Last 24 hours", ms: 24 * 3600e3 },
  };

  function parseRange(value) {
    return Object.prototype.hasOwnProperty.call(RANGES, value) ? value : "1h";
  }

  // CLASSES are the outcome classes of /v1/history, in chart order. A Go
  // test keeps their outcomes equal to the Gateway's (history.ClassOf);
  // "other" holds every outcome not listed.
  const CLASSES = [
    { key: "ok", label: "ok", cls: "ok", outcomes: ["ok"] },
    { key: "toolError", label: "Tool errors", cls: "warn", outcomes: ["tool_error", "rpc_error"] },
    { key: "upstreamError", label: "Upstream failures", cls: "bad", outcomes: ["upstream_error", "upstream_unreachable", "timeout"] },
    { key: "other", label: "Refused and other", cls: "info", outcomes: [] },
  ];

  // The top lists show TOP_N entries; the error-rate lists only those with
  // at least TOP_MIN_CALLS calls in the range (SPEC-0024-R06).
  const TOP_N = 10;
  const TOP_MIN_CALLS = 20;

  // historyPath is the /v1/history request for a range and scope.
  function historyPath(range, route, tool) {
    const query = new URLSearchParams({ range: parseRange(range) });
    if (route) query.set("route", route);
    if (route && tool) query.set("tool", tool);
    return "/v1/history?" + query.toString();
  }

  function num(value) {
    return typeof value === "number" && Number.isFinite(value) ? value : null;
  }

  function time(value) {
    const t = Date.parse(value);
    return Number.isNaN(t) ? null : t;
  }

  function points(history) {
    return (history && Array.isArray(history.points) ? history.points : []).filter((p) => p && time(p.time) !== null);
  }

  // UNITS format a chart's values.
  const UNITS = {
    perMin: (v) => (v === null ? "–" : (v < 10 ? Math.round(v * 10) / 10 : Math.round(v)) + "/min"),
    pct: (v) => fmtPct(v),
    ms: (v) => (v === 0 ? "0 ms" : fmtMs(v)),
    bytesPerSec: (v) => (v === null ? "–" : fmtBytes(Math.round(v)) + "/s"),
  };

  // chartSeries turns a /v1/history answer into the dashboard's charts:
  // requests per minute by outcome class (stacked), error rate, p50 and p95
  // latency, and bytes per second, which a tool has none of.
  function chartSeries(history) {
    const list = points(history);
    const step = Math.max(1, Number(history && history.stepSeconds) || 60);
    const at = (p) => time(p.time);
    const charts = [
      {
        id: "requests", title: "Requests per minute", kind: "bars", unit: "perMin",
        series: CLASSES.map((c) => ({ key: c.key, label: c.label, cls: c.cls, values: list.map((p) => ({ t: at(p), v: (num(p[c.key]) || 0) * 60 / step })) })),
      },
      {
        id: "errors", title: "Error rate", kind: "lines", unit: "pct", max: 1,
        series: [{ key: "errorRate", label: "Error rate", cls: "bad", values: list.map((p) => ({ t: at(p), v: num(p.errorRate) })) }],
      },
      {
        id: "latency", title: "Latency", kind: "lines", unit: "ms",
        series: [
          { key: "p50Ms", label: "p50", cls: "accent", values: list.map((p) => ({ t: at(p), v: num(p.p50Ms) })) },
          { key: "p95Ms", label: "p95", cls: "warn", values: list.map((p) => ({ t: at(p), v: num(p.p95Ms) })) },
        ],
      },
    ];
    if (list.some((p) => num(p.bytesIn) !== null)) {
      charts.push({
        id: "bytes", title: "Bytes per second", kind: "lines", unit: "bytesPerSec",
        series: [
          { key: "bytesIn", label: "In", cls: "accent", values: list.map((p) => ({ t: at(p), v: num(p.bytesIn) === null ? null : p.bytesIn / step })) },
          { key: "bytesOut", label: "Out", cls: "info", values: list.map((p) => ({ t: at(p), v: num(p.bytesOut) === null ? null : p.bytesOut / step })) },
        ],
      });
    }
    return charts;
  }

  // niceMax rounds a maximum up to 1, 2, or 5 times a power of ten.
  function niceMax(max) {
    if (!(max > 0)) return 1;
    const power = Math.pow(10, Math.floor(Math.log10(max)));
    for (const m of [1, 2, 5, 10]) if (m * power >= max) return m * power;
    return 10 * power;
  }

  // TICK_STEPS are the distances of time ticks to choose from.
  const TICK_STEPS = [5, 10, 15, 30, 60, 120, 180, 360].map((m) => m * 60e3);

  // chartGeometry lays a chart out in a box of width × height pixels from
  // start to end (ms): y scale and ticks, x ticks, the stacked bars or line
  // paths, the area before since (no data), and x positions of the points
  // for hover. Lines break where a value is null.
  function chartGeometry(chart, opts) {
    const width = Math.max(120, Math.floor(opts.width));
    const height = opts.height || 160;
    const pad = { top: 8, right: 8, bottom: 22, left: 52 };
    const plot = { x: pad.left, y: pad.top, w: width - pad.left - pad.right, h: height - pad.top - pad.bottom };
    const start = opts.start;
    const end = Math.max(opts.end, start + 1);
    const stepMs = (opts.stepSeconds || 60) * 1000;
    const x = (t) => plot.x + ((t - start) / (end - start)) * plot.w;
    const first = chart.series[0] ? chart.series[0].values : [];

    let max = 0;
    if (chart.kind === "bars") {
      first.forEach((_, i) => { max = Math.max(max, chart.series.reduce((sum, s) => sum + (s.values[i].v || 0), 0)); });
    } else {
      for (const s of chart.series) for (const p of s.values) if (p.v !== null) max = Math.max(max, p.v);
    }
    const top = chart.max ? Math.min(chart.max, niceMax(max)) : niceMax(max);
    const y = (v) => plot.y + plot.h - (Math.min(v, top) / top) * plot.h;
    const fmt = UNITS[chart.unit] || String;
    const yTicks = [0, top / 2, top].map((v) => ({ y: y(v), label: fmt(v) }));

    const span = end - start;
    const tickStep = TICK_STEPS.find((s) => span / s <= 6) || TICK_STEPS[TICK_STEPS.length - 1];
    const xTicks = [];
    for (let t = Math.ceil(start / tickStep) * tickStep; t <= end; t += tickStep) xTicks.push({ t, x: x(t) });

    const bars = [];
    const lines = [];
    if (chart.kind === "bars") {
      const w = Math.max(1, (stepMs / span) * plot.w - 1);
      first.forEach((p, i) => {
        let base = 0;
        for (const s of chart.series) {
          const v = s.values[i].v || 0;
          if (v > 0) bars.push({ key: s.key, cls: s.cls, x: x(p.t), y: y(base + v), w, h: y(base) - y(base + v) });
          base += v;
        }
      });
    } else {
      for (const s of chart.series) {
        let d = "";
        let open = false;
        for (const p of s.values) {
          if (p.v === null) { open = false; continue; }
          const px = round(x(p.t + stepMs / 2));
          const py = round(y(p.v));
          // A point alone is drawn as a dot: a zero-length segment with
          // round caps.
          d += open ? " L" + px + " " + py : (d ? " " : "") + "M" + px + " " + py + " l0 0";
          open = true;
        }
        lines.push({ key: s.key, cls: s.cls, d });
      }
    }
    const since = opts.since && opts.since > start ? opts.since : null;
    return {
      width, height, plot, top, yTicks, xTicks, bars, lines,
      before: since ? { x: plot.x, w: Math.max(0, x(since) - plot.x) } : null,
      pointX: first.map((p) => x(p.t + stepMs / 2)),
      x,
    };
  }

  function round(v) {
    return Math.round(v * 10) / 10;
  }

  // nearestIndex is the point closest to pixel px, or -1 without points.
  function nearestIndex(geometry, px) {
    let best = -1;
    let distance = Infinity;
    geometry.pointX.forEach((x, i) => {
      const d = Math.abs(x - px);
      if (d < distance) { distance = d; best = i; }
    });
    return best;
  }

  // moveIndex moves a keyboard cursor: arrows by one, Home and End to the
  // ends, PageUp and PageDown by ten.
  function moveIndex(index, key, count) {
    if (!count) return -1;
    const current = index < 0 ? count - 1 : index;
    const next = { ArrowLeft: current - 1, ArrowRight: current + 1, PageUp: current - 10, PageDown: current + 10, Home: 0, End: count - 1 }[key];
    return next === undefined ? current : Math.max(0, Math.min(count - 1, next));
  }

  // pointValues lists a chart's values at index i, formatted.
  function pointValues(chart, i) {
    const fmt = UNITS[chart.unit] || String;
    return chart.series.map((s) => ({ label: s.label, cls: s.cls, text: fmt(s.values[i] ? s.values[i].v : null) }));
  }

  // clockLabel shows a time as local HH:MM.
  function clockLabel(t) {
    const d = new Date(t);
    return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  }

  // readout is the text shown for the point under the cursor.
  function readout(chart, i) {
    const p = chart.series[0] && chart.series[0].values[i];
    if (!p) return "";
    return clockLabel(p.t) + " · " + pointValues(chart, i).map((v) => v.label + " " + v.text).join(", ");
  }

  // tableRows are the same numbers as the chart, for screen readers
  // (SPEC-0024-R08): a time and one formatted value per series.
  function tableRows(chart) {
    const first = chart.series[0] ? chart.series[0].values : [];
    return first.map((p, i) => [clockLabel(p.t), ...pointValues(chart, i).map((v) => v.text)]);
  }

  // alertMarks are the starts and resolutions of alerts inside the range
  // (SPEC-0024-R09), oldest first. /v1/history already limits the alerts to
  // the scope.
  function alertMarks(alerts, start, end) {
    const marks = [];
    for (const alert of Array.isArray(alerts) ? alerts : []) {
      if (!alert || typeof alert.name !== "string") continue;
      const since = time(alert.since);
      const resolved = alert.resolvedAt ? time(alert.resolvedAt) : null;
      if (since !== null && since >= start && since <= end) marks.push({ t: since, kind: "start", alert });
      if (resolved !== null && resolved >= start && resolved <= end) marks.push({ t: resolved, kind: "resolved", alert });
    }
    return marks.sort((a, b) => a.t - b.t);
  }

  function totalName(total) {
    return String(total.route || "") + "\u0000" + String(total.tool || "");
  }

  // topLists ranks route or tool totals: the TOP_N with the most calls, and
  // the TOP_N with the highest error rate among those with at least
  // TOP_MIN_CALLS calls and any error (SPEC-0024-R06).
  function topLists(totals, n = TOP_N, minCalls = TOP_MIN_CALLS) {
    const list = (Array.isArray(totals) ? totals : []).filter((t) => t && num(t.calls) > 0);
    const busiest = [...list].sort((a, b) => b.calls - a.calls || totalName(a).localeCompare(totalName(b))).slice(0, n);
    const failing = list.filter((t) => t.calls >= minCalls && num(t.errorRate) > 0)
      .sort((a, b) => b.errorRate - a.errorRate || b.calls - a.calls || totalName(a).localeCompare(totalName(b))).slice(0, n);
    return { busiest, failing };
  }

  // rangeSummary totals the points: calls, the share not ok, and calls per
  // class.
  function rangeSummary(history) {
    const sums = Object.fromEntries(CLASSES.map((c) => [c.key, 0]));
    for (const p of points(history)) for (const c of CLASSES) sums[c.key] += num(p[c.key]) || 0;
    const calls = CLASSES.reduce((n, c) => n + sums[c.key], 0);
    return { calls, errorRate: calls ? (calls - sums.ok) / calls : null, ...sums };
  }

  // toolRows is the per-tool table of a server's Dashboard tab (SPEC-0024-R07):
  // every discovered tool and every tool with calls in the range, busiest
  // first, then by name.
  function toolRows(totals, discovered) {
    const rows = new Map();
    for (const name of Array.isArray(discovered) ? discovered : []) rows.set(name, { tool: name, calls: 0, errors: 0, errorRate: null, p95Ms: null });
    for (const t of Array.isArray(totals) ? totals : []) {
      if (t && typeof t.tool === "string") rows.set(t.tool, { tool: t.tool, calls: num(t.calls) || 0, errors: num(t.errors) || 0, errorRate: num(t.errorRate), p95Ms: num(t.p95Ms) });
    }
    return [...rows.values()].sort((a, b) => b.calls - a.calls || a.tool.localeCompare(b.tool));
  }

  // dashboardNote says whose numbers these are and since when
  // (SPEC-0024-R10).
  function dashboardNote(history, now = Date.now()) {
    const replica = history && typeof history.instance === "string" && history.instance ? "Gateway replica " + history.instance : "This Gateway replica";
    const since = history ? time(history.since) : null;
    const start = history ? time(history.start) : null;
    if (since !== null && start !== null && since > start) {
      const minutes = Math.max(0, Math.round((now - since) / 60e3));
      const age = minutes < 60 ? minutes + " min" : Math.floor(minutes / 60) + " h " + (minutes % 60) + " min";
      return replica + " only, since it started " + age + " ago (" + clockLabel(since) + ") · kept in memory, lost on restart";
    }
    return replica + " only · kept in memory for up to 24 hours, lost on restart";
  }

  // GRAFANA names the dashboards the chart installs (SPEC-0023).
  const GRAFANA = {
    traffic: { uid: "kavrynt-mcp-traffic", title: "Kavrynt MCP Traffic" },
    server: { uid: "kavrynt-mcp-server", title: "Kavrynt MCP Server" },
  };

  // grafanaLinks links the Grafana dashboards for cluster-wide history: the
  // traffic dashboard, and the server dashboard for a route. Only http and
  // https base URLs are linked; without one it returns null.
  function grafanaLinks(base, route) {
    if (typeof base !== "string" || !/^https?:\/\/[^\s/?#]+[^\s?#]*$/i.test(base)) return null;
    const root = base.replace(/\/+$/, "");
    const links = [{ title: GRAFANA.traffic.title, href: root + "/d/" + GRAFANA.traffic.uid }];
    const dot = typeof route === "string" ? route.indexOf(".") : -1;
    if (dot > 0) {
      const query = new URLSearchParams({ "var-namespace": route.slice(0, dot), "var-name": route.slice(dot + 1) });
      links.push({ title: GRAFANA.server.title, href: root + "/d/" + GRAFANA.server.uid + "?" + query.toString() });
    }
    return links;
  }

  Object.assign(exports, {
    RANGES, parseRange, CLASSES, TOP_N, TOP_MIN_CALLS, UNITS, GRAFANA, historyPath, chartSeries, niceMax, chartGeometry,
    nearestIndex, moveIndex, pointValues, clockLabel, readout, tableRows, alertMarks, topLists, rangeSummary, toolRows,
    dashboardNote, grafanaLinks,
  });
})(
  typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).charts = {}),
  typeof module === "object" && module.exports ? require("./format.js") : globalThis.Kavrynt.format,
);
