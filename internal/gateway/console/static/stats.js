// Everything the console computes from /v1/servers and /v1/traffic: server
// state, attention lists, per-tool statistics, the tool catalog, filters, and
// the diagnostics text. Pure functions with no DOM access, so they run under
// Node's test runner (test/console) as well as in the browser.
//
// Server and traffic data are untrusted. Nothing here renders them; views
// insert them as text only.
"use strict";

// Shared by the browser (Kavrynt.stats) and Node's test runner (module.exports).
((exports) => {
  // TRAFFIC_WINDOW is how many requests /v1/traffic keeps per Gateway replica.
  const TRAFFIC_WINDOW = 500;

  // SCOPE_NOTE qualifies every view that shows traffic (SPEC-0002-R09).
  const SCOPE_NOTE = "This Gateway replica only · last " + TRAFFIC_WINDOW + " requests · use /metrics for history";

  // OUTCOMES defines every outcome the Gateway records, in the words of the
  // Gateway component documentation. A Go test keeps the keys equal to the
  // Gateway's outcome constants.
  const OUTCOMES = {
    ok: {
      cls: "ok",
      definition: "The server answered without a JSON-RPC or tool error. Event-stream responses are not inspected and count as ok unless the HTTP status is an error.",
    },
    tool_error: {
      cls: "warn",
      definition: "The tool ran and reported a failure (result.isError). The server is reachable; check the tool's input or the data it reads.",
    },
    rpc_error: {
      cls: "warn",
      definition: "The server answered with a JSON-RPC error, for example an unknown method or invalid parameters.",
    },
    client_error: {
      cls: "warn",
      definition: "The server answered with HTTP 4xx, for example 405 when a client opens the optional GET event stream and the server does not offer one.",
    },
    upstream_error: {
      cls: "bad",
      definition: "The server answered with HTTP 5xx.",
    },
    upstream_unreachable: {
      cls: "bad",
      definition: "The Gateway could not reach the endpoint: the name did not resolve, the connection was refused, or the address is one the egress policy refuses. Check the endpoint, its Service, and its pods.",
    },
    timeout: {
      cls: "bad",
      definition: "No complete response arrived within the Gateway request timeout (30 s by default; event streams only until their headers).",
    },
    incomplete: {
      cls: "bad",
      definition: "The response started but could not be copied to the client, usually because the client disconnected.",
    },
    gateway_error: {
      cls: "bad",
      definition: "The Gateway did not proxy the request, for example because the endpoint points back at a Gateway.",
    },
  };

  // Server health thresholds (SPEC-0002-R19).
  const FAILING_MIN_REQUESTS = 5;
  const FAILING_MIN_RATE = 0.5;

  function isFailure(record) {
    return record.outcome !== "ok";
  }

  function outcomeClass(outcome) {
    return (OUTCOMES[outcome] && OUTCOMES[outcome].cls) || "bad";
  }

  function percentile(values, p) {
    if (!values.length) return null;
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))];
  }

  // summarize returns call count, failures, error rate, latency percentiles,
  // and the newest time of records (newest first, as /v1/traffic orders them).
  function summarize(records) {
    const errors = records.filter(isFailure).length;
    const latencies = records.map((r) => r.durationMs);
    return {
      calls: records.length,
      errors,
      rate: records.length ? errors / records.length : null,
      p50: percentile(latencies, 0.5),
      p95: percentile(latencies, 0.95),
      last: records.length ? records[0].time : null,
    };
  }

  function groupBy(records, key) {
    const groups = new Map();
    for (const r of records) {
      const k = key(r);
      if (!groups.has(k)) groups.set(k, []);
      groups.get(k).push(r);
    }
    return groups;
  }

  // operation names what a request did: the tool for tools/call, otherwise the
  // JSON-RPC method, so tools/list and resources/list failures stay visible.
  function operation(record) {
    return record.tool && record.tool !== "none" ? record.tool : record.method;
  }

  // toolSummary is the per-tool table of SPEC-0002-R07: one row per route and
  // operation, busiest first.
  function toolSummary(records) {
    const rows = [];
    for (const rs of groupBy(records, (r) => r.route + "\u0000" + operation(r)).values()) {
      rows.push({ route: rs[0].route, name: operation(rs[0]), isTool: rs[0].tool !== "none", ...summarize(rs) });
    }
    return rows.sort((a, b) => b.calls - a.calls || b.errors - a.errors || a.name.localeCompare(b.name));
  }

  function routeSummary(records) {
    const rows = [];
    for (const [route, rs] of groupBy(records, (r) => r.route)) rows.push({ route, ...summarize(rs) });
    return rows.sort((a, b) => b.calls - a.calls);
  }

  // failingTools lists the tools with the most failed calls (SPEC-0002-R03).
  // Tools without failures are never listed.
  function failingTools(records, limit = 5) {
    return toolSummary(records.filter((r) => r.tool && r.tool !== "none"))
      .filter((row) => row.errors > 0)
      .sort((a, b) => b.errors - a.errors || b.rate - a.rate)
      .slice(0, limit);
  }

  // filterTraffic applies the Traffic view filters (SPEC-0002-R06). outcome is
  // "", "errors" (anything but ok), or one outcome value.
  function filterTraffic(records, f = {}) {
    return records.filter((r) =>
      (!f.route || r.route === f.route) &&
      (!f.method || r.method === f.method) &&
      (!f.tool || r.tool === f.tool) &&
      (!f.outcome || (f.outcome === "errors" ? isFailure(r) : r.outcome === f.outcome)));
  }

  function distinct(records, field) {
    return [...new Set(records.map((r) => r[field]))].filter((v) => v && v !== "none").sort();
  }

  // ---------------------------------------------------------------- servers

  function condition(server, type) {
    return (server.conditions || []).find((c) => c.type === type) || null;
  }

  function discoveryFailed(server) {
    const discovered = condition(server, "ToolsDiscovered");
    return Boolean(discovered && discovered.status === "False");
  }

  // serverState is a server's routing state as the Operator and Gateway report
  // it, without traffic.
  function serverState(server) {
    if (server.routed) return { cls: "ok", label: "Routed", detail: "The Gateway routes this server." };
    const ready = condition(server, "Ready");
    if (!ready) return { cls: "warn", label: "Pending", detail: "The Operator has not reported this server yet." };
    if (ready.status === "True") return { cls: "warn", label: "Syncing", detail: "Ready, waiting for the Gateway route." };
    const cls = ready.reason === "InvalidSpec" ? "bad" : "warn";
    const labels = { InvalidSpec: "Invalid spec", UnsupportedTransport: "Not proxied (" + server.transport + ")", NamespaceNotWatched: "Namespace not watched" };
    return { cls, label: labels[ready.reason] || ready.reason || "Not ready", detail: ready.message };
  }

  // serverHealth adds what the server's recent traffic says to its state
  // (SPEC-0002-R19): a routed server whose tool discovery failed, or whose
  // recent requests mostly fail, needs attention.
  function serverHealth(server, traffic) {
    const stats = summarize(traffic.filter((r) => r.route === server.route));
    const state = serverState(server);
    const reasons = [];
    if (server.routed) {
      if (stats.calls >= FAILING_MIN_REQUESTS && stats.rate >= FAILING_MIN_RATE) {
        const top = topOutcome(traffic.filter((r) => r.route === server.route && isFailure(r)));
        reasons.push(stats.errors + " of its last " + stats.calls + " requests failed, mostly " + top + ".");
      }
      if (discoveryFailed(server)) {
        reasons.push("Tool discovery failed: " + (condition(server, "ToolsDiscovered").message || "no message") + ".");
      }
    }
    if (!reasons.length) return { ...state, attention: !server.routed, stats };
    const failing = stats.calls >= FAILING_MIN_REQUESTS && stats.rate >= FAILING_MIN_RATE;
    return { cls: failing ? "bad" : "warn", label: "Needs attention", detail: reasons.join(" "), attention: true, stats };
  }

  function topOutcome(records) {
    let best = null;
    for (const [outcome, rs] of groupBy(records, (r) => r.outcome)) {
      if (!best || rs.length > best.n) best = { outcome, n: rs.length };
    }
    return best ? best.outcome : "–";
  }

  // attention lists every server the Overview must surface (SPEC-0002-R02,
  // R19): not routed, tool discovery failed, or failing traffic.
  function attention(servers, traffic) {
    const rank = { bad: 0, warn: 1, ok: 2 };
    return servers
      .map((server) => ({ server, health: serverHealth(server, traffic) }))
      .filter((item) => item.health.attention)
      .sort((a, b) => rank[a.health.cls] - rank[b.health.cls] || a.server.route.localeCompare(b.server.route));
  }

  // overview returns the Overview tiles (SPEC-0002-R01).
  function overview(servers, traffic) {
    const stats = summarize(traffic);
    return {
      servers: servers.length,
      namespaces: new Set(servers.map((s) => s.namespace)).size,
      routed: servers.filter((s) => s.routed).length,
      tools: servers.reduce((n, s) => n + (s.tools || []).length, 0),
      requests: stats.calls,
      errorRate: stats.rate,
      p95: stats.p95,
    };
  }

  // filterServers applies the Servers view filters. state is "", "routed",
  // "not-routed", or "attention".
  function filterServers(servers, traffic, f = {}) {
    const term = (f.q || "").trim().toLowerCase();
    return servers.filter((s) => {
      if (f.ns && s.namespace !== f.ns) return false;
      if (f.state === "routed" && !s.routed) return false;
      if (f.state === "not-routed" && s.routed) return false;
      if (f.state === "attention" && !serverHealth(s, traffic).attention) return false;
      if (!term) return true;
      return s.route.toLowerCase().includes(term) || (s.description || "").toLowerCase().includes(term) ||
        (s.tools || []).some((t) => t.name.toLowerCase().includes(term));
    });
  }

  // versionInfo compares the declared spec.version with the version the
  // server reported in MCP initialize (SPEC-0004-R04). A difference is shown,
  // never acted on: it does not change server health.
  function versionInfo(server) {
    const declared = server.version || "";
    const info = server.serverInfo || null;
    const reported = info && info.version ? String(info.version) : "";
    return {
      declared,
      reported,
      reportedName: info && info.name ? String(info.name) : "",
      differs: Boolean(declared && reported && declared !== reported),
    };
  }

  // ---------------------------------------------------------------- tools

  // catalog lists every discovered tool of every server (SPEC-0002-R04). A tool
  // can be tried only through a routed server (SPEC-0002-R05).
  function catalog(servers) {
    const entries = [];
    for (const server of servers) {
      for (const tool of server.tools || []) {
        entries.push({
          tool: tool.name,
          description: tool.description || "",
          route: server.route,
          routed: server.routed,
          state: serverState(server),
          playable: server.routed,
          reason: server.routed ? "" : "Not routed: " + serverState(server).label + ". The Gateway does not proxy calls to this server.",
        });
      }
    }
    return entries.sort((a, b) => a.tool.localeCompare(b.tool) || a.route.localeCompare(b.route));
  }

  function searchCatalog(entries, q) {
    const term = (q || "").trim().toLowerCase();
    if (!term) return entries;
    return entries.filter((e) => e.tool.toLowerCase().includes(term) || e.description.toLowerCase().includes(term) || e.route.toLowerCase().includes(term));
  }

  // ---------------------------------------------------------------- diagnostics

  // stripQuery drops the query string and fragment of an endpoint, which may
  // carry credentials or tokens.
  function stripQuery(endpoint) {
    if (!endpoint) return "";
    return String(endpoint).split(/[?#]/)[0];
  }

  // redactQuery replaces the endpoint's query string and fragment wherever
  // they appear in text. Condition messages quote the endpoint URL in full.
  function redactQuery(text, endpoint) {
    const raw = String(endpoint || "");
    const cut = raw.search(/[?#]/);
    if (cut < 0) return text;
    return String(text).split(raw.slice(cut)).join("?[redacted]");
  }

  // diagnostics is the plain-text summary behind "Copy diagnostics"
  // (SPEC-0002-R11). It holds metadata only: never the endpoint's query string,
  // tool inputs, or responses.
  function diagnostics(server, traffic, consoleVersion, now = new Date()) {
    const records = traffic.filter((r) => r.route === server.route);
    const stats = summarize(records);
    const health = serverHealth(server, traffic);
    const lines = [
      "Kavrynt console diagnostics",
      "Generated: " + now.toISOString(),
      "Console version: " + (consoleVersion || "unknown"),
      "",
      "Route: /mcp/" + server.route,
      "Namespace: " + server.namespace,
      "Name: " + server.name,
      "Declared version: " + server.version,
      "Reported: " + (versionInfo(server).reported ? versionInfo(server).reportedName + " " + versionInfo(server).reported : "–"),
      "Transport: " + server.transport,
      "Endpoint: " + (stripQuery(server.endpoint) || "–"),
      "Routed: " + (server.routed ? "yes" : "no"),
      "State: " + health.label + (health.detail ? " — " + health.detail : ""),
      "",
      "Conditions:",
    ];
    for (const c of server.conditions || []) {
      lines.push("  " + c.type + "=" + c.status + " reason=" + c.reason + " since=" + c.lastTransitionTime + (c.message ? " message=" + JSON.stringify(c.message) : ""));
    }
    if (!(server.conditions || []).length) lines.push("  (none reported)");
    lines.push("", "Tools (" + (server.tools || []).length + "): " + ((server.tools || []).map((t) => t.name).join(", ") || "–"));
    lines.push("", "Recent traffic (" + SCOPE_NOTE + "):");
    if (!records.length) lines.push("  (none)");
    else {
      lines.push("  requests=" + stats.calls + " errors=" + stats.errors + " p50Ms=" + round(stats.p50) + " p95Ms=" + round(stats.p95));
      for (const row of toolSummary(records)) {
        lines.push("  " + row.name + ": calls=" + row.calls + " errors=" + row.errors + " p95Ms=" + round(row.p95));
      }
      lines.push("  Last requests (time method tool outcome status ms):");
      for (const r of records.slice(0, 20)) {
        lines.push("    " + [r.time, r.method, r.tool, r.outcome, r.status, round(r.durationMs)].join(" "));
      }
    }
    return redactQuery(lines.join("\n") + "\n", server.endpoint);
  }

  function round(ms) {
    return ms === null || ms === undefined ? "–" : String(Math.round(ms * 100) / 100);
  }

  Object.assign(exports, { TRAFFIC_WINDOW, SCOPE_NOTE, OUTCOMES, FAILING_MIN_REQUESTS, FAILING_MIN_RATE, isFailure, outcomeClass, percentile, summarize, operation, toolSummary, routeSummary, failingTools, filterTraffic, distinct, condition, serverState, serverHealth, versionInfo, attention, overview, filterServers, catalog, searchCatalog, stripQuery, diagnostics });
})(typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).stats = {}));
