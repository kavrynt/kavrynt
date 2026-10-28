// Kavrynt Console: read-only views of the Gateway's JSON endpoints
// (/v1/servers, /v1/traffic, /v1/alerts, /v1/history, /readyz, /version) and a playground that calls
// MCP tools through the Gateway's own /mcp/ routes.
//
// Everything shown here that comes from MCP servers (tool names,
// descriptions, schemas, results) is untrusted. It is only ever inserted as
// text (textContent), never as HTML.
"use strict";

(() => {
  const { h, svg, put, clear, toast, copy, copyRow, badge, codeBlock, emptyState, tile, panel, scopeNote } = Kavrynt.dom;
  const { pretty, fmtBytes, fmtMs, fmtPct, ago, clock, toolHints, connectSnippets } = Kavrynt.format;
  const {
    scopeNote: trafficScope, OUTCOMES, outcomeClass, condition, serverHealth, versionInfo, attention, overview, failingTools, toolSummary,
    filterTraffic, filterServers, distinct, catalog, searchCatalog, diagnostics, stripQuery,
    ALERTS, sortAlerts, activeAlertCount, alertLink, alertTitle, isRunbookURL,
  } = Kavrynt.stats;
  const {
    RANGES, GRAFANA, parseRange, historyPath, chartSeries, chartGeometry, nearestIndex, moveIndex, readout, tableRows, clockLabel,
    alertMarks, topLists, rangeSummary, toolRows, dashboardNote, grafanaLinks,
  } = Kavrynt.charts;
  const { parseHash, formatHash, isLoopbackHost, createPoller, sessionState, loginMessage, notice } = Kavrynt.state;
  const { renderPlayground, closePlayground, resetPlayground } = Kavrynt.playground;

  const SERVER_POLL_MS = 5000;
  const TRAFFIC_POLL_MS = 2000;
  const TRAFFIC_PAGE = 100;
  const HISTORY_POLL_MS = 30000;
  const RESIZE_DELAY_MS = 150;
  const THEME_KEY = "kavrynt-theme";

  const state = {
    servers: [],
    serversLoaded: false,
    serversError: null,
    traffic: [],
    // Pod name of the Gateway replica whose recent traffic is shown.
    trafficInstance: "",
    // /v1/alerts (SPEC-0020): the alerts and the replica that evaluated them.
    alerts: [],
    alertsInstance: "",
    alertsLoaded: false,
    alertsError: null,
    // /v1/history (SPEC-0024): historyPath is the request of the dashboard
    // on screen ("" polls nothing); history is the answer to historyFor.
    history: null,
    historyFor: "",
    historyPath: "",
    historyError: null,
    seenTrafficId: 0,
    freshIds: new Set(),
    trafficPaused: false,
    trafficHideBefore: 0,
    version: null,
    ready: null,
    routes: null,
    // Console admin login (SPEC-0005). checked is false until GET
    // /v1/session answers; nothing is drawn or fetched before that.
    auth: { checked: false, enabled: false, signedIn: false, username: "", expiresAt: "", mcpAuth: false },
  };

  const main = () => document.getElementById("main");

  function gatewayURL(route) {
    return location.origin + "/mcp/" + route;
  }

  // ---------------------------------------------------------------- data

  async function getJSON(path) {
    const res = await fetch(path, { headers: { Accept: "application/json" }, cache: "no-store" });
    if (res.status === 401 && state.auth.enabled) {
      signedOut("Your session ended. Sign in again.");
      throw new Error("login required");
    }
    if (!res.ok) throw new Error(path + " returned HTTP " + res.status);
    return res.json();
  }

  // needsLogin is true while the console must show only the login form.
  function needsLogin() {
    return !state.auth.checked || (state.auth.enabled && !state.auth.signedIn);
  }

  async function refreshServers() {
    if (needsLogin()) return;
    try {
      const data = await getJSON("/v1/servers");
      state.servers = data.servers || [];
      state.serversError = null;
    } catch (err) {
      state.serversError = err.message;
    }
    state.serversLoaded = true;
    update("servers");
  }

  async function refreshAlerts() {
    if (needsLogin()) return;
    try {
      const data = await getJSON("/v1/alerts");
      state.alerts = Array.isArray(data.alerts) ? data.alerts : [];
      state.alertsInstance = typeof data.instance === "string" ? data.instance : "";
      state.alertsError = null;
    } catch (err) {
      state.alertsError = err.message;
    }
    state.alertsLoaded = true;
    drawAlertCount();
    update("alerts");
  }

  // drawAlertCount shows the number of active alerts in the navigation on
  // every page (SPEC-0020-R07).
  function drawAlertCount() {
    const el = document.getElementById("alert-count");
    const count = needsLogin() ? 0 : activeAlertCount(state.alerts);
    el.textContent = String(count);
    el.hidden = count === 0;
    el.closest("a").setAttribute("aria-label", count ? "Alerts, " + count + " active" : "Alerts");
  }

  // refreshHistory loads /v1/history for the dashboard on screen, if any
  // (SPEC-0024-R06). An answer for a scope no longer shown is dropped.
  async function refreshHistory() {
    const path = state.historyPath;
    if (!path || needsLogin()) return;
    try {
      const data = await getJSON(path);
      if (path !== state.historyPath) return;
      Object.assign(state, { history: data, historyFor: path, historyError: null });
    } catch (err) {
      if (path !== state.historyPath) return;
      state.historyError = err.message;
    }
    update("history");
  }

  // showHistory points the history poll at path and loads it; a history of
  // another range or scope is not shown meanwhile.
  function showHistory(path) {
    state.historyPath = path;
    if (state.historyFor !== path) Object.assign(state, { history: null, historyFor: "", historyError: null });
    refreshHistory();
  }

  async function refreshTraffic() {
    if (state.trafficPaused || needsLogin()) return;
    try {
      const data = await getJSON("/v1/traffic");
      const records = data.requests || [];
      state.trafficInstance = typeof data.instance === "string" ? data.instance : "";
      state.freshIds = new Set(records.filter((r) => r.id > state.seenTrafficId && state.seenTrafficId > 0).map((r) => r.id));
      if (records.length) state.seenTrafficId = Math.max(state.seenTrafficId, records[0].id);
      state.traffic = records;
    } catch { /* the status indicator reports Gateway problems */ }
    update("traffic");
  }

  async function refreshStatus() {
    const el = document.getElementById("gw-status");
    try {
      const res = await fetch("/readyz", { cache: "no-store" });
      const body = await res.json();
      state.ready = res.ok;
      state.routes = body.routes;
      put(clear(el), h("span", { class: "dot " + (res.ok ? "ok" : "bad") }),
        h("span", { text: res.ok ? "Gateway ready · " + body.routes + (body.routes === 1 ? " route" : " routes") : "Gateway not ready" }));
    } catch {
      state.ready = false;
      put(clear(el), h("span", { class: "dot bad" }), h("span", { text: "Gateway unreachable" }));
    }
    update("status");
  }

  // ---------------------------------------------------------------- login

  async function checkSession() {
    let next = null;
    try {
      const res = await fetch("/v1/session", { headers: { Accept: "application/json" }, cache: "no-store" });
      next = sessionState(res.status, await res.json().catch(() => null));
    } catch { /* treated as login required below */ }
    // Fail closed: an unexpected answer shows the login form, not data.
    Object.assign(state.auth, next || { enabled: true, signedIn: false, username: "", expiresAt: "" }, { checked: true });
    drawUser();
    initNotice();
  }

  // signedOut forgets everything the session showed, so no server, tool,
  // traffic, or Playground data stays on screen or in memory (SPEC-0005-R14).
  function signedOut(message) {
    const wasSignedIn = state.auth.signedIn;
    Object.assign(state.auth, { signedIn: false, username: "", expiresAt: "" });
    Object.assign(state, { servers: [], serversLoaded: false, serversError: null, traffic: [], seenTrafficId: 0, freshIds: new Set(),
      alerts: [], alertsInstance: "", alertsLoaded: false, alertsError: null, history: null, historyFor: "", historyPath: "", historyError: null });
    resetPlayground();
    drawUser();
    drawAlertCount();
    render();
    if (wasSignedIn && message) toast(message);
  }

  async function signedIn() {
    await checkSession();
    render();
    refreshServers();
    refreshTraffic();
    refreshAlerts();
  }

  async function logout() {
    try { await fetch("/v1/session", { method: "DELETE", cache: "no-store" }); } catch { /* the cookie also expires */ }
    signedOut("Signed out.");
  }

  // drawUser shows the signed-in user and Log out (SPEC-0005-R15).
  function drawUser() {
    const el = document.getElementById("user");
    clear(el);
    el.hidden = !(state.auth.enabled && state.auth.signedIn);
    if (el.hidden) return;
    put(el, h("span", { class: "user-name", text: state.auth.username, title: state.auth.expiresAt ? "Session ends " + new Date(state.auth.expiresAt).toLocaleString() : undefined }),
      h("button", { type: "button", class: "linkish", onclick: logout }, "Log out"));
  }

  function renderLogin(root) {
    const user = h("input", { type: "text", id: "login-user", name: "username", value: "admin", autocomplete: "username", required: true });
    const password = h("input", { type: "password", id: "login-password", name: "password", autocomplete: "current-password", required: true });
    const error = h("p", { class: "error-text", role: "alert" });
    const submit = h("button", { type: "submit", class: "btn primary" }, "Sign in");
    const form = h("form", {
      class: "login",
      onsubmit: async (e) => {
        e.preventDefault();
        error.textContent = "";
        submit.disabled = true;
        let res = null;
        try {
          res = await fetch("/v1/session", {
            method: "POST", cache: "no-store",
            headers: { "Content-Type": "application/json", Accept: "application/json" },
            body: JSON.stringify({ username: user.value, password: password.value }),
          });
        } catch { /* reported below */ }
        // The password lives only in the form field (SPEC-0005-R15).
        password.value = "";
        submit.disabled = false;
        if (res && res.status === 204) {
          signedIn();
          return;
        }
        error.textContent = loginMessage(res ? res.status : 0, res && res.headers.get("Retry-After"));
        password.focus();
      },
    },
    h("h1", { text: "Sign in" }),
    h("p", { class: "muted", text: "Sign in as admin to see MCP servers, tools, and traffic." }),
    h("div", { class: "field" }, h("label", { for: "login-user", text: "Username" }), user),
    h("div", { class: "field" }, h("label", { for: "login-password", text: "Password" }), password),
    error,
    submit,
    h("details", { class: "login-help" },
      h("summary", { text: "Where is the password?" }),
      h("p", { text: "Helm generates it into a Secret when it installs Kavrynt. Read it with:" }),
      codeBlock("kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin \\\n  -o jsonpath='{.data.password}' | base64 -d"),
      h("p", { class: "muted small", text: "Use your release namespace and name if they differ, or the Secret set in gateway.console.auth.existingSecret." })));
    put(root, h("div", { class: "login-wrap" }, form));
    setTimeout(() => password.focus(), 0);
  }

  async function loadVersion() {
    try {
      state.version = await getJSON("/version");
      document.getElementById("gw-version").textContent = "v" + (state.version.version || "dev");
    } catch { /* optional */ }
    update("status");
  }

  // ---------------------------------------------------------------- router

  let current = { page: "", update: null };

  const views = {
    overview: (root) => renderOverview(root),
    dashboard: (root, rest, params) => renderDashboard(root, params),
    servers: (root, rest, params) => (rest[0] ? renderServer(root, rest[0], rest[1] || "overview", params) : renderServers(root, params)),
    tools: (root, rest, params) => renderTools(root, params),
    playground: (root, rest) => renderPlayground(root, rest[0], rest[1], {
      state, refreshTraffic, rerender: render, isCurrent: () => parseHash(location.hash).page === "playground",
    }),
    traffic: (root, rest, params) => renderTraffic(root, params),
    alerts: (root) => renderAlerts(root),
  };

  function render() {
    if (needsLogin()) {
      document.querySelectorAll("[data-nav]").forEach((a) => { a.classList.remove("active"); a.removeAttribute("aria-current"); });
      const root = clear(main());
      current = { page: "login", update: null };
      if (state.auth.checked) renderLogin(root);
      else put(root, emptyState("Loading…"));
      return;
    }
    const { page: requested, rest, params } = parseHash(location.hash);
    const page = views[requested] ? requested : "overview";
    document.querySelectorAll("[data-nav]").forEach((a) => {
      const active = a.dataset.nav === page;
      a.classList.toggle("active", active);
      if (active) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    const root = clear(main());
    current = { page, update: null };
    // Only a dashboard view polls history; it sets the path again.
    state.historyPath = "";
    current.update = views[page](root, rest, params) || null;
    root.focus({ preventScroll: true });
  }

  // update redraws the current view after a poll. Views rebuild their content,
  // so focus moves back to the control with the same link, label, or id.
  function update(what) {
    if (!current.update) return;
    const el = document.activeElement;
    const key = el && el !== document.body && focusKey(el);
    current.update(what);
    if (key && !el.isConnected) {
      const match = [...main().querySelectorAll("a, button, select, input, textarea, [tabindex]")].find((c) => focusKey(c) === key);
      if (match) match.focus({ preventScroll: true });
    }
  }

  function focusKey(el) {
    return el.getAttribute("href") || el.getAttribute("aria-label") || el.id || null;
  }

  // setParams records a view's filters in the URL without a new history entry
  // or a redraw, so reloading or sharing the URL restores the view.
  function setParams(params) {
    const { page, rest } = parseHash(location.hash);
    history.replaceState(null, "", formatHash(page || "overview", rest, params));
  }

  // ---------------------------------------------------------------- shared

  function healthBadge(health) {
    return badge(health.label, health.cls, health.detail || undefined);
  }

  function toolsBadge(server) {
    const discovered = condition(server, "ToolsDiscovered");
    if (discovered && discovered.status === "True") return badge(server.tools.length + (server.tools.length === 1 ? " tool" : " tools"), "info");
    if (discovered && discovered.reason === "DiscoveryFailed") return badge("Tool discovery failed", "warn");
    return null;
  }

  function rateCell(rate) {
    const pct = rate === null ? 0 : rate * 100;
    return h("td", { class: "rate" },
      h("span", { class: "num-label", text: fmtPct(rate) }),
      h("span", { class: "bar" + (pct > 0 ? " bad" : ""), "aria-hidden": "true" }, h("span", { style: "width:" + Math.max(pct, pct > 0 ? 3 : 0) + "%" })));
  }

  function table(headers, rows, empty) {
    return h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, headers.map((c) => h("th", { scope: "col", class: c.num ? "num" : "", text: c.label || c })))),
      h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: String(headers.length), class: "muted", text: empty || "Nothing to show." })))));
  }

  // toolSummaryPanel is the per-tool table of SPEC-0002-R07.
  function toolSummaryPanel(records, showRoute) {
    const rows = toolSummary(records).map((row) => h("tr", {},
      h("td", { class: "mono" }, row.isTool ? row.name : h("span", { class: "muted", text: row.name })),
      showRoute ? h("td", { class: "mono", text: row.route }) : null,
      h("td", { class: "num", text: String(row.calls) }),
      h("td", { class: "num", text: String(row.errors) }),
      rateCell(row.rate),
      h("td", { class: "num", text: fmtMs(row.p50) }),
      h("td", { class: "num", text: fmtMs(row.p95) }),
      h("td", { text: ago(row.last), title: row.last })));
    const headers = ["Tool or method", showRoute ? "Route" : null, { label: "Calls", num: true }, { label: "Errors", num: true }, "Error rate",
      { label: "p50", num: true }, { label: "p95", num: true }, "Last call"].filter(Boolean);
    return panel("Per tool", h("div", { class: "body flush" }, table(headers, rows, "No requests match.")),
      h("span", { class: "muted small", text: "Methods without a tool, such as tools/list, are shown in grey." }));
  }

  // trafficTable lists requests, at most limit of them (SPEC-0002-R20).
  function trafficTable(records, opts) {
    if (!records.length) {
      return panel("Requests", emptyState("No traffic yet", opts.empty || "Call a tool from the Playground or point your agent at a Gateway route. Requests appear here within two seconds."));
    }
    const shown = records.slice(0, opts.limit);
    const rows = shown.map((r) => h("tr", { class: state.freshIds.has(r.id) ? "fresh" : "" },
      h("td", { class: "mono", text: clock(r.time), title: r.time }),
      opts.showRoute ? h("td", { class: "mono", text: r.route }) : null,
      h("td", { class: "mono", text: r.method }),
      h("td", { class: "mono", text: r.tool === "none" ? "–" : r.tool }),
      h("td", {}, badge(r.outcome, outcomeClass(r.outcome), OUTCOMES[r.outcome] ? OUTCOMES[r.outcome].definition : undefined)),
      h("td", { class: "num", text: String(r.status || "–") }),
      h("td", { class: "num", text: fmtMs(r.durationMs) }),
      h("td", { class: "num", text: fmtBytes(r.bytesIn) }),
      h("td", { class: "num", text: fmtBytes(r.bytesOut) })));
    const headers = ["Time", opts.showRoute ? "Route" : null, "Method", "Tool", "Outcome", { label: "HTTP", num: true }, { label: "Latency", num: true },
      { label: "In", num: true }, { label: "Out", num: true }].filter(Boolean);
    const more = records.length > shown.length ? h("div", { class: "more" },
      h("span", { class: "muted", text: "Showing " + shown.length + " of " + records.length + " requests." }),
      h("button", { type: "button", class: "btn small", onclick: () => opts.onMore(opts.limit + TRAFFIC_PAGE) }, "Show " + Math.min(TRAFFIC_PAGE, records.length - shown.length) + " more"),
      h("button", { type: "button", class: "btn small", onclick: () => opts.onMore(records.length) }, "Show all")) : null;
    return panel("Requests", h("div", { class: "body flush" }, table(headers, rows), more),
      h("span", { class: "muted small", text: records.length + (records.length === 1 ? " request" : " requests") }));
  }

  function outcomeHelp() {
    return h("details", { class: "outcomes" },
      h("summary", { text: "What do the outcomes mean?" }),
      h("dl", { class: "kv" }, Object.entries(OUTCOMES).map(([name, o]) => [
        h("dt", {}, badge(name, o.cls)), h("dd", { text: o.definition })])));
  }

  function select(label, value, options, onchange) {
    const el = h("select", { "aria-label": label, onchange: (e) => onchange(e.target.value) },
      options.map((o) => h("option", { value: o.value }, o.label)));
    el.value = options.some((o) => o.value === value) ? value : "";
    return el;
  }

  // ---------------------------------------------------------------- overview

  function renderOverview(root) {
    const head = h("div", { class: "page-head" },
      h("div", {}, h("h1", { text: "Overview" }), h("p", { text: "What needs attention across the MCP servers this Gateway watches." })));
    const tiles = h("div", { class: "tiles" });
    const body = h("div");
    put(root, head, tiles, body);

    function draw() {
      const o = overview(state.servers, state.traffic);
      const gateway = state.ready === null ? "…" : state.ready ? "Ready" : "Not ready";
      put(clear(tiles),
        tile("Gateway", gateway, "version " + ((state.version && state.version.version) || "unknown"), state.ready === false ? "bad" : ""),
        tile("Servers", o.servers, o.namespaces + (o.namespaces === 1 ? " namespace" : " namespaces")),
        tile("Routed", o.routed + " of " + o.servers, o.servers - o.routed + " not routed"),
        tile("Tools", o.tools, "discovered by the Operator"),
        tile("Requests", o.requests, o.requests ? fmtPct(o.errorRate) + " errors · p95 " + fmtMs(o.p95) : "no traffic yet"));

      clear(body);
      if (state.serversError) {
        put(body, emptyState("Cannot load servers", state.serversError));
        return;
      }
      const items = attention(state.servers, state.traffic);
      const attentionBody = items.length ? h("ul", { class: "attention" }, items.map(({ server, health }) => h("li", {},
        h("a", { href: formatHash("servers", [server.route]), class: "attention-link" },
          h("span", { class: "dot " + (health.cls === "bad" ? "bad" : "warn") }),
          h("span", { class: "mono", text: server.route }),
          badge(health.label, health.cls)),
        h("p", { class: "muted", text: health.detail || "" })))) :
        h("p", { class: "body muted", text: state.serversLoaded ? "Every server is routed, its tools were discovered, and its recent requests succeed." : "Loading…" });

      const failing = failingTools(state.traffic);
      const failingRows = failing.map((row) => h("tr", {},
        h("td", { class: "mono", text: row.name }),
        h("td", {}, h("a", { class: "mono", href: formatHash("traffic", [], { route: row.route, tool: row.name }), text: row.route })),
        h("td", { class: "num", text: row.errors + " of " + row.calls }),
        rateCell(row.rate),
        h("td", { class: "num", text: fmtMs(row.p95) })));

      const alertCount = activeAlertCount(state.alerts);
      put(body,
        panel("Needs attention", attentionBody, h("span", { class: "panel-actions" },
          items.length ? badge(String(items.length), "warn") : badge("none", "ok"),
          h("a", { href: formatHash("alerts"), text: alertCount ? alertCount + (alertCount === 1 ? " active alert →" : " active alerts →") : "Alerts →" }))),
        h("section", { class: "panel" },
          h("header", {}, h("h2", { text: "Failing tools" }), h("a", { href: formatHash("traffic", [], { outcome: "errors" }), text: "All errors →" })),
          scopeNote(trafficScope(state.trafficInstance)),
          h("div", { class: "body flush" }, failing.length ?
            table(["Tool", "Route", { label: "Failed", num: true }, "Error rate", { label: "p95", num: true }], failingRows) :
            h("p", { class: "body muted", text: state.traffic.length ? "No tool call failed in the recent requests." : "No traffic yet." }))));
    }

    draw();
    return draw;
  }

  // ---------------------------------------------------------------- dashboard

  // renderDashboard charts this replica's calls over the last hour, 6
  // hours, or day for all servers, one server, or one tool, with the
  // busiest and most failing servers and tools (SPEC-0024-R06). The range
  // and scope are in the URL.
  function renderDashboard(root, params) {
    const filters = { range: parseRange(params.range), route: params.route || "", tool: (params.route && params.tool) || "" };
    const toolbar = h("div", { class: "toolbar" });
    const body = h("div");
    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "Dashboard" }),
          h("p", { text: "MCP traffic over time: requests, errors, latency, and bytes, with the console alerts marked." })),
        toolbar),
      body);
    const dash = dashboard(body, () => filters, { extra: (history) => topPanels(history, filters.range) });
    let toolbarKey = "";

    function setFilter(key, value) {
      filters[key] = value;
      if (key === "route") filters.tool = "";
      setParams(filters);
      drawToolbar();
      dash.load();
    }

    function drawToolbar() {
      const routes = state.servers.filter((s) => s.routed).map((s) => s.route);
      if (filters.route) routes.push(filters.route);
      const server = state.servers.find((s) => s.route === filters.route);
      const tools = (server ? server.tools.map((t) => t.name) : []).concat(filters.tool ? [filters.tool] : []);
      const options = (values, all) => [{ value: "", label: all }].concat([...new Set(values)].sort().map((v) => ({ value: v, label: v })));
      const key = JSON.stringify([routes, tools, filters]);
      if (key === toolbarKey) return;
      toolbarKey = key;
      put(clear(toolbar),
        select("Range", filters.range, Object.entries(RANGES).map(([value, r]) => ({ value, label: r.label })), (v) => setFilter("range", v)),
        select("Server", filters.route, options(routes, "All servers"), (v) => setFilter("route", v)),
        filters.route ? select("Tool", filters.tool, options(tools, "All tools"), (v) => setFilter("tool", v)) : null);
    }

    drawToolbar();
    dash.load();
    return (what) => {
      if (what === "servers") drawToolbar();
      dash.update(what);
    };
  }

  // dashboard draws /v1/history for the scope get() returns into container:
  // the replica note, totals, charts with alert marks, the alerts of the
  // range, and opts.extra (SPEC-0024-R06 to R10).
  function dashboard(container, get, opts) {
    // Where each chart's cursor is, kept across polls and resizes.
    const cursors = new Map();

    function load() {
      const { range, route, tool } = get();
      showHistory(historyPath(range, route, tool));
      draw();
    }

    function draw() {
      clear(container);
      const history = state.historyFor === state.historyPath ? state.history : null;
      if (state.historyError && !history) {
        put(container, emptyState("Cannot load history", state.historyError));
        return;
      }
      if (!history) {
        put(container, emptyState("Loading…"));
        return;
      }
      const { range, route } = get();
      const start = Date.parse(history.start);
      const end = Date.parse(history.end);
      const since = Date.parse(history.since);
      const summary = rangeSummary(history);
      const links = grafanaLinks(history.grafanaURL, route);
      put(container,
        h("p", { class: "scope-note" }, dashboardNote(history), " · cluster-wide history: ",
          links ? links.map((link, i) => [i ? ", " : "", h("a", { href: link.href, target: "_blank", rel: "noopener noreferrer", text: link.title })]) :
            "the " + GRAFANA.traffic.title + " and " + GRAFANA.server.title + " Grafana dashboards (gateway.metrics.dashboard.enabled)"),
        h("div", { class: "tiles" },
          tile("Calls", summary.calls, RANGES[range].label.toLowerCase()),
          tile("Error rate", fmtPct(summary.errorRate), "calls not ok", summary.errorRate > 0 ? "warn" : ""),
          tile("Tool errors", summary.toolError, "tool_error, rpc_error"),
          tile("Upstream failures", summary.upstreamError, "upstream_error, unreachable, timeout", summary.upstreamError ? "bad" : "")));
      const marks = alertMarks(history.alerts, start, end);
      const grid = h("div", { class: "chart-grid" });
      put(container, grid);
      // Every panel is in the grid before any chart measures its width.
      const hosts = chartSeries(history).map((chart) => {
        const host = h("div", { class: "chart-host" });
        put(grid, panel(chart.title, h("div", { class: "body" }, legend(chart), host)));
        return { host, chart };
      });
      for (const { host, chart } of hosts) drawChart(host, chart, { start, end, since, stepSeconds: history.stepSeconds, marks }, cursors);
      if (marks.length) put(container, rangeAlertsPanel(marks));
      if (opts.extra) put(container, opts.extra(history));
    }

    return {
      load,
      update: (what) => { if (what === "history" || what === "resize") draw(); },
    };
  }

  function legend(chart) {
    return h("ul", { class: "legend", "aria-hidden": "true" }, chart.series.map((s) =>
      h("li", {}, h("span", { class: "swatch c-" + s.cls }), s.label)));
  }

  // drawChart draws a chart as inline SVG at its host's width, with a
  // cursor that follows the pointer and the arrow keys, a readout of the
  // values under it, and a table of the same numbers for screen readers
  // (SPEC-0024-R08). Alert starts and resolutions are dashed lines
  // (SPEC-0024-R09).
  function drawChart(host, chart, ctx, cursors) {
    const geometry = chartGeometry(chart, { width: host.clientWidth || 320, height: 170, start: ctx.start, end: ctx.end, since: ctx.since, stepSeconds: ctx.stepSeconds });
    const { plot } = geometry;
    const count = geometry.pointX.length;
    const out = h("p", { class: "chart-readout", "aria-live": "polite" });
    const cursor = svg("line", { class: "cursor", x1: 0, x2: 0, y1: plot.y, y2: plot.y + plot.h, visibility: "hidden" });
    let index = cursors.has(chart.id) ? Math.min(cursors.get(chart.id), count - 1) : -1;

    function show(i) {
      index = i;
      cursors.set(chart.id, i);
      if (i < 0) {
        cursor.setAttribute("visibility", "hidden");
        out.textContent = count ? "Hover over the chart or focus it and use the arrow keys to read values." : "No calls recorded in this range yet.";
        return;
      }
      cursor.setAttribute("x1", geometry.pointX[i]);
      cursor.setAttribute("x2", geometry.pointX[i]);
      cursor.setAttribute("visibility", "visible");
      out.textContent = readout(chart, i);
    }

    const el = svg("svg", {
      class: "chart", width: geometry.width, height: geometry.height, viewBox: "0 0 " + geometry.width + " " + geometry.height,
      role: "img", tabindex: "0", "aria-label": chart.title + " chart; arrow keys read its values",
      onmousemove: (e) => {
        const box = el.getBoundingClientRect();
        show(nearestIndex(geometry, ((e.clientX - box.left) * geometry.width) / (box.width || geometry.width)));
      },
      onmouseleave: () => { if (document.activeElement !== el) show(-1); },
      onfocus: () => show(index < 0 ? count - 1 : index),
      onblur: () => show(-1),
      onkeydown: (e) => {
        if (!["ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"].includes(e.key)) return;
        e.preventDefault();
        show(moveIndex(index, e.key, count));
      },
    },
    geometry.before ? svg("rect", { class: "before-since", x: geometry.before.x, y: plot.y, width: geometry.before.w, height: plot.h },
      svg("title", {}, "No history: before this replica started")) : null,
    geometry.yTicks.map((t) => [
      svg("line", { class: "grid", x1: plot.x, x2: plot.x + plot.w, y1: t.y, y2: t.y }),
      svg("text", { class: "tick", x: plot.x - 6, y: t.y + 4, "text-anchor": "end" }, t.label)]),
    geometry.xTicks.map((t) => svg("text", { class: "tick", x: t.x, y: geometry.height - 6, "text-anchor": "middle" }, clockLabel(t.t))),
    geometry.bars.map((b) => svg("rect", { class: "column c-" + b.cls, x: b.x, y: b.y, width: b.w, height: b.h })),
    geometry.lines.map((l) => svg("path", { class: "line c-" + l.cls, d: l.d })),
    ctx.marks.map((m) => svg("line", { class: "mark mark-" + m.kind, x1: geometry.x(m.t), x2: geometry.x(m.t), y1: plot.y, y2: plot.y + plot.h },
      svg("title", {}, alertTitle(m.alert) + (m.kind === "start" ? " started " : " resolved ") + clockLabel(m.t)))),
    cursor);

    // Tables ignore width and overflow, so the wrapper is what is hidden.
    const table = h("div", { class: "sr-only" }, h("table", {},
      h("caption", { text: chart.title }),
      h("thead", {}, h("tr", {}, h("th", { scope: "col", text: "Time" }), chart.series.map((s) => h("th", { scope: "col", text: s.label })))),
      h("tbody", {}, tableRows(chart).map((row) => h("tr", {}, row.map((cell) => h("td", { text: cell })))))));
    put(clear(host), el, out, table);
    show(index);
  }

  // rangeAlertsPanel lists the alert starts and resolutions the charts mark.
  function rangeAlertsPanel(marks) {
    return panel("Alerts in this range", h("ul", { class: "alert-list" }, marks.map((m) => {
      const scope = m.alert.scope || {};
      const link = alertLink(m.alert);
      return h("li", { class: "alert-item" + (m.kind === "resolved" ? " resolved" : "") },
        h("div", { class: "alert-line" },
          h("span", { class: "dot " + (m.kind === "resolved" ? "ok" : "warn") }),
          h("strong", { text: alertTitle(m.alert) }),
          h("span", { class: "muted", text: (m.kind === "start" ? "started " : "resolved ") + clockLabel(m.t) }),
          h("a", { class: "mono", href: formatHash(link.page, link.rest, link.params), text: String(scope.route || "") + (scope.tool ? " · " + scope.tool : "") })));
    })), h("a", { href: formatHash("alerts"), text: "Alerts →" }));
  }

  // topPanels are the busiest and most failing servers (for all servers)
  // and tools (unless one tool is shown), linking to their server's or
  // tool's dashboard (SPEC-0024-R06).
  function topPanels(history, range) {
    const scope = history.scope || {};
    if (scope.tool) return null;
    const panels = [];
    if (!scope.route) {
      const servers = topLists(history.routes);
      panels.push(topPanel("Busiest servers", servers.busiest, range, "No calls in this range."),
        topPanel("Highest error rate: servers", servers.failing, range, "No server with 20 calls or more had errors."));
    }
    const tools = topLists(history.tools);
    panels.push(topPanel("Busiest tools", tools.busiest, range, "No tool calls in this range."),
      topPanel("Highest error rate: tools", tools.failing, range, "No tool with 20 calls or more had errors."));
    return h("div", { class: "top-grid" }, panels);
  }

  function topPanel(title, rows, range, empty) {
    const withTools = rows.some((r) => r.tool);
    const body = rows.map((r) => h("tr", {},
      h("td", {}, h("a", { class: "mono", href: formatHash("servers", [r.route, "dashboard"], { range, tool: r.tool || "" }), text: r.tool || r.route })),
      withTools ? h("td", { class: "mono muted", text: r.route }) : null,
      h("td", { class: "num", text: String(r.calls) }),
      rateCell(typeof r.errorRate === "number" ? r.errorRate : null),
      h("td", { class: "num", text: fmtMs(r.p95Ms) })));
    const headers = [withTools ? "Tool" : "Server", withTools ? "Server" : null, { label: "Calls", num: true }, "Error rate", { label: "p95", num: true }].filter(Boolean);
    return panel(title, h("div", { class: "body flush" }, table(headers, body, empty)));
  }

  // serverDashboardExtra is the per-tool table of a server's Dashboard tab,
  // or, with a tool shown, the way back to all tools (SPEC-0024-R07).
  function serverDashboardExtra(history, route, filters) {
    if (filters.tool) {
      return h("p", {}, h("a", { href: formatHash("servers", [route, "dashboard"], { range: filters.range }), text: "← All tools of " + route }));
    }
    const server = state.servers.find((s) => s.route === route);
    const rows = toolRows(history.tools, server ? server.tools.map((t) => t.name) : []).map((r) => h("tr", {},
      h("td", {}, h("a", { class: "mono", href: formatHash("servers", [route, "dashboard"], { range: filters.range, tool: r.tool }), text: r.tool })),
      h("td", { class: "num", text: String(r.calls) }),
      h("td", { class: "num", text: String(r.errors) }),
      rateCell(r.errorRate),
      h("td", { class: "num", text: fmtMs(r.p95Ms) })));
    return panel("Per tool", h("div", { class: "body flush" },
      table(["Tool", { label: "Calls", num: true }, { label: "Errors", num: true }, "Error rate", { label: "p95", num: true }], rows, "No tools discovered and no tool calls in this range.")),
    h("span", { class: "muted small", text: RANGES[filters.range].label }));
  }

  // ---------------------------------------------------------------- alerts

  // renderAlerts lists active alerts, then those resolved in the last hour,
  // each linking its server or tool and its runbook (SPEC-0020-R06).
  function renderAlerts(root) {
    const head = h("div", { class: "page-head" },
      h("div", {}, h("h1", { text: "Alerts" }),
        h("p", { text: "Problems the Gateway sees now, with the same conditions and thresholds as the Prometheus alerts." })));
    const body = h("div");
    put(root, head, body);

    function alertRow(alert) {
      const link = alertLink(alert);
      const scope = alert.scope || {};
      const definition = ALERTS[alert.name] ? ALERTS[alert.name].definition : undefined;
      return h("li", { class: "alert-item" + (alert.resolvedAt ? " resolved" : "") },
        h("div", { class: "alert-line" },
          h("span", { class: "dot " + (alert.resolvedAt ? "ok" : alert.severity === "critical" ? "bad" : "warn") }),
          h("strong", { text: alertTitle(alert), title: definition }),
          badge(String(alert.severity || ""), alert.resolvedAt ? "" : alert.severity === "critical" ? "bad" : "warn"),
          h("a", { class: "mono", href: formatHash(link.page, link.rest, link.params), text: String(scope.route || "") + (scope.tool ? " · " + scope.tool : "") })),
        h("p", { text: String(alert.summary || "") }),
        h("p", { class: "muted small" },
          h("span", { text: "Since " + ago(alert.since), title: String(alert.since || "") }),
          alert.resolvedAt ? h("span", { text: " · resolved " + ago(alert.resolvedAt), title: String(alert.resolvedAt) }) : null,
          isRunbookURL(alert.runbookURL) ? [" · ", h("a", { href: alert.runbookURL, target: "_blank", rel: "noopener noreferrer", text: "Runbook" })] : null));
    }

    function draw() {
      clear(body);
      if (state.alertsError) {
        put(body, emptyState("Cannot load alerts", state.alertsError));
        return;
      }
      if (!state.alertsLoaded) {
        put(body, emptyState("Loading…"));
        return;
      }
      const { active, resolved } = sortAlerts(state.alerts);
      const scope = "Server and discovery alerts come from MCPServer status and are the same on every replica. Traffic alerts count " +
        (state.alertsInstance ? "Gateway replica " + state.alertsInstance : "this Gateway replica") + "'s calls only; Prometheus alerts sum every replica.";
      put(body,
        scopeNote(scope),
        panel("Active", active.length ? h("ul", { class: "alert-list" }, active.map(alertRow)) :
          h("p", { class: "body muted", text: "No active alerts." }), badge(String(active.length), active.length ? "warn" : "ok")),
        panel("Resolved in the last hour", resolved.length ? h("ul", { class: "alert-list" }, resolved.map(alertRow)) :
          h("p", { class: "body muted", text: "Nothing resolved in the last hour." })));
    }

    draw();
    return (what) => { if (what === "alerts") draw(); };
  }

  // ---------------------------------------------------------------- servers

  function renderServers(root, params) {
    const filters = { q: params.q || "", ns: params.ns || "", state: params.state || "" };
    const tiles = h("div", { class: "tiles" });
    const list = h("div");
    const nsSelect = h("select", { "aria-label": "Namespace", onchange: (e) => { filters.ns = e.target.value; changed(); } });
    const search = h("input", {
      type: "search", placeholder: "Search servers or tools", "aria-label": "Search servers or tools", value: filters.q,
      oninput: (e) => { filters.q = e.target.value; changed(); },
    });
    const stateSelect = select("State", filters.state, [
      { value: "", label: "All states" }, { value: "attention", label: "Needs attention" },
      { value: "routed", label: "Routed" }, { value: "not-routed", label: "Not routed" }], (v) => { filters.state = v; changed(); });

    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "MCP servers" }), h("p", { text: "Every MCPServer the Gateway watches, its health, and the tools the Operator discovered." })),
        h("div", { class: "toolbar" }, search, nsSelect, stateSelect)),
      tiles, list);

    function changed() {
      setParams(filters);
      draw();
    }

    function drawTiles() {
      const o = overview(state.servers, state.traffic);
      const needing = attention(state.servers, state.traffic).length;
      put(clear(tiles),
        tile("Servers", o.servers, o.namespaces + (o.namespaces === 1 ? " namespace" : " namespaces")),
        tile("Needs attention", needing, needing ? "see the cards marked below" : "all healthy", needing ? "warn" : ""),
        tile("Tools", o.tools, "discovered by the Operator"),
        tile("Recent requests", o.requests, o.requests ? fmtPct(o.errorRate) + " errors" : "no traffic yet"));
    }

    function draw() {
      drawTiles();
      const servers = state.servers;
      const namespaces = [...new Set(servers.map((s) => s.namespace))].sort();
      put(clear(nsSelect), h("option", { value: "" }, "All namespaces"), namespaces.map((ns) => h("option", { value: ns }, ns)));
      nsSelect.value = namespaces.includes(filters.ns) ? filters.ns : "";

      clear(list);
      if (state.serversError) {
        put(list, emptyState("Cannot load servers", state.serversError));
        return;
      }
      if (!state.serversLoaded) {
        put(list, emptyState("Loading…"));
        return;
      }
      if (!servers.length) {
        put(list, emptyState("No MCP servers yet", "Register one with kubectl or kavryctl. It appears here within seconds.",
          "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata:\n  name: my-mcp-server\n  namespace: default\nspec:\n  version: 1.0.0\n  transport: http\n  endpoint: http://my-mcp-server.default.svc.cluster.local:8080/mcp\n\n# kubectl apply -f my-mcp-server.yaml\n# or: kavryctl register my-mcp-server.yaml"));
        return;
      }
      const shown = filterServers(servers, state.traffic, filters);
      if (!shown.length) {
        put(list, emptyState("No servers match the filters"));
        return;
      }
      put(list, h("div", { class: "grid" }, shown.map(serverCard)));
    }

    draw();
    return draw;
  }

  // versionCell shows the declared version, the reported one when the
  // Operator recorded it, and a badge when they differ (SPEC-0004-R04).
  function versionCell(server) {
    const v = versionInfo(server);
    return h("dd", { class: "version", title: "Declared in spec.version" + (v.reported ? "; reported by " + (v.reportedName || "the server") + " in MCP initialize" : "") },
      v.declared,
      v.reported && !v.differs ? h("span", { class: "muted", text: " · reported" }) : null,
      v.differs ? [h("span", { class: "muted", text: " · reports " + v.reported + " " }), badge("version differs", "warn")] : null);
  }

  function serverCard(server) {
    const health = serverHealth(server, state.traffic);
    const href = formatHash("servers", [server.route]);
    const stats = health.stats;
    return h("a", { class: "card clickable state-" + health.cls, href, "data-route": server.route, "aria-label": server.route + ", " + health.label },
      h("div", { class: "card-head" },
        h("div", {}, h("h3", { text: server.name }), h("div", { class: "ns", text: server.namespace })),
        healthBadge(health)),
      server.description ? h("p", { class: "muted card-desc", text: server.description }) : null,
      health.label === "Needs attention" ? h("p", { class: "card-alert " + health.cls, text: health.detail }) : null,
      h("dl", {},
        h("dt", { text: "Transport" }), h("dd", { text: server.transport }),
        h("dt", { text: "Version" }), versionCell(server),
        h("dt", { text: "Endpoint" }), h("dd", { text: stripQuery(server.endpoint) || "–", title: stripQuery(server.endpoint) }),
        h("dt", { text: "Tools" }), h("dd", {}, toolsBadge(server) || h("span", { class: "muted", text: "–" })),
        h("dt", { text: "Traffic" }), h("dd", { text: stats.calls ? stats.calls + " recent · " + fmtPct(stats.rate) + " errors" : "no recent requests" })));
  }

  // ---------------------------------------------------------------- server detail

  function renderServer(root, routeName, tab, params = {}) {
    const body = h("div");
    put(root, body);
    let limit = TRAFFIC_PAGE;
    let shown = false;
    // The Dashboard tab (SPEC-0024-R07) keeps its charts across redraws of
    // the page head.
    const filters = { range: parseRange(params.range), tool: params.tool || "" };
    const dashToolbar = h("div", { class: "toolbar dash-toolbar" });
    const dashBox = h("div");
    const dash = tab === "dashboard" ? dashboard(dashBox, () => ({ range: filters.range, route: routeName, tool: filters.tool }), {
      extra: (history) => serverDashboardExtra(history, routeName, filters),
    }) : null;

    function drawDashToolbar() {
      const server = state.servers.find((s) => s.route === routeName);
      const tools = [...new Set((server ? server.tools.map((t) => t.name) : []).concat(filters.tool ? [filters.tool] : []))].sort();
      put(clear(dashToolbar),
        select("Range", filters.range, Object.entries(RANGES).map(([value, r]) => ({ value, label: r.label })), (v) => setDashFilter("range", v)),
        select("Tool", filters.tool, [{ value: "", label: "All tools" }].concat(tools.map((t) => ({ value: t, label: t }))), (v) => setDashFilter("tool", v)));
    }

    function setDashFilter(key, value) {
      filters[key] = value;
      setParams({ range: filters.range, tool: filters.tool });
      drawDashToolbar();
      dash.load();
    }

    function draw() {
      const server = state.servers.find((s) => s.route === routeName);
      clear(body);
      shown = Boolean(server);
      if (!server) {
        put(body, h("nav", { class: "crumbs", "aria-label": "Breadcrumb" }, h("a", { href: "#/servers", text: "Servers" })),
          emptyState(state.serversLoaded ? "Server not found" : "Loading…", state.serversLoaded ? "No MCPServer is named " + routeName + "." : ""));
        return;
      }
      const health = serverHealth(server, state.traffic);
      put(body,
        h("nav", { class: "crumbs", "aria-label": "Breadcrumb" }, h("a", { href: "#/servers", text: "Servers" }), " / ", server.namespace, " / ", server.name),
        h("div", { class: "page-head" },
          h("div", {}, h("h1", { text: server.name }),
            h("div", { class: "badges", style: "margin-top:6px" }, healthBadge(health), toolsBadge(server), badge(server.transport), badge("v" + server.version, "", "spec.version declared in the MCPServer"),
            versionInfo(server).differs ? badge("reports v" + versionInfo(server).reported, "warn", "The server reported a different version in MCP initialize") : null)),
          h("div", { class: "toolbar" },
            h("button", { type: "button", class: "btn", onclick: () => copy(diagnostics(server, state.traffic, state.version && state.version.version), "Diagnostics copied: metadata only, no tool inputs or results") }, "Copy diagnostics"),
            server.routed ? h("a", { class: "btn primary", href: formatHash("playground", [server.route]) }, "Open in playground") : null)),
        health.label === "Needs attention" ? h("div", { class: "callout " + health.cls, role: "note" }, h("strong", { text: "Needs attention. " }), health.detail) : null,
        h("div", { class: "tabs", role: "tablist" },
          tabButton("overview", "Overview"), tabButton("dashboard", "Dashboard"), tabButton("tools", "Tools (" + server.tools.length + ")"),
          tabButton("traffic", "Traffic (" + health.stats.calls + ")")));

      if (tab === "dashboard") {
        drawDashToolbar();
        put(body, dashToolbar, dashBox);
      } else if (tab === "tools") put(body, toolsPanel(server));
      else if (tab === "traffic") {
        const records = state.traffic.filter((r) => r.route === server.route);
        put(body, scopeNote(trafficScope(state.trafficInstance)),
          records.length ? toolSummaryPanel(records, false) : null,
          trafficTable(records, { showRoute: false, limit, onMore: (n) => { limit = n; draw(); }, empty: "No requests to this route in the recent traffic." }),
          records.length ? h("p", {}, h("a", { href: formatHash("traffic", [], { route: server.route }), text: "Filter in the Traffic view →" })) : null);
      } else put(body, serverOverview(server, health));
    }

    function tabButton(id, label) {
      return h("button", {
        type: "button", role: "tab", class: tab === id ? "active" : "", "aria-selected": String(tab === id),
        onclick: () => { location.hash = formatHash("servers", [routeName, id]); },
      }, label);
    }

    draw();
    if (dash) dash.load();
    return (what) => {
      if (dash) {
        if (what === "servers" && !shown) {
          draw();
          // Charts drawn while the tab was detached had no width yet.
          if (shown) dash.update("resize");
        }
        dash.update(what);
        return;
      }
      if (what === "servers" || (what === "traffic" && tab === "traffic")) draw();
    };
  }

  function serverOverview(server, health) {
    const url = gatewayURL(server.route);
    const conditions = table(["Type", "Status", "Reason", "Message", "Changed"], server.conditions.map((c) => h("tr", {},
      h("td", { text: c.type }),
      h("td", {}, badge(c.status, c.status === "True" ? "ok" : c.status === "False" ? (c.type === "Ready" || c.type === "Accepted" ? "bad" : "warn") : "")),
      h("td", { text: c.reason }),
      h("td", { class: "wrap", text: c.message }),
      h("td", { text: ago(c.lastTransitionTime), title: c.lastTransitionTime }))), "The Operator has not reported conditions yet.");

    const summary = h("dl", { class: "kv" },
      h("dt", { text: "Namespace" }), h("dd", { text: server.namespace }),
      h("dt", { text: "Route" }), h("dd", { class: "mono", text: "/mcp/" + server.route }),
      h("dt", { text: "Endpoint" }), h("dd", { class: "mono", text: server.endpoint || "–" }),
      h("dt", { text: "Declared version" }), h("dd", { text: server.version }),
      h("dt", { text: "Reported" }), h("dd", { text: versionInfo(server).reported ? versionInfo(server).reportedName + " " + versionInfo(server).reported + (versionInfo(server).differs ? " (differs from the declared version)" : "") : "– (recorded after the next successful tool discovery)" }),
      h("dt", { text: "Created" }), h("dd", { text: ago(server.createdAt), title: server.createdAt }),
      server.description ? [h("dt", { text: "Description" }), h("dd", { text: server.description })] : null);

    const panels = [
      panel("Summary", h("div", { class: "body stack" }, summary, health.detail && health.label !== "Needs attention" ? h("p", { class: "muted", style: "margin:10px 0 0", text: health.detail }) : null), healthBadge(health)),
      panel("Conditions", h("div", { class: "body flush" }, conditions)),
    ];
    if (server.routed) panels.push(connectPanel(server, url));
    return h("div", {}, panels);
  }

  function connectPanel(server, url) {
    const snippets = connectSnippets(server.name, url);
    const names = Object.keys(snippets);
    let active = names[0];
    const tabs = h("div", { class: "tabs", role: "tablist", style: "margin-bottom:10px" });
    const out = h("div");
    function draw() {
      put(clear(tabs), names.map((n) => h("button", { type: "button", role: "tab", "aria-selected": String(n === active), class: n === active ? "active" : "", onclick: () => { active = n; draw(); } }, n)));
      put(clear(out), codeBlock(snippets[active]),
        h("div", { style: "margin-top:8px" }, h("button", { class: "btn small", type: "button", onclick: () => copy(snippets[active]) }, "Copy snippet")));
    }
    draw();
    return panel("Connect your agent", h("div", { class: "body stack" },
      h("p", { class: "muted", style: "margin:0", text: "Point any MCP client (Streamable HTTP) at this Gateway URL. Calls show up under Traffic." }),
      copyRow(url), tabs, out,
      h("p", { class: "muted small", style: "margin:0", text: "The Gateway does not authenticate callers yet and never forwards Authorization or Cookie headers upstream." })));
  }

  // hintBadges shows the behavior hints the server reported for a tool
  // (SPEC-0028-R08), or nothing when it reported none.
  function hintBadges(annotations) {
    const labels = toolHints(annotations);
    if (!labels.length) return null;
    const why = "Reported by the MCP server, not verified by Kavrynt";
    return h("div", { class: "badges" }, labels.map((label) => badge(label, label === "destructive" ? "warn" : "", why)));
  }

  function toolsPanel(server) {
    if (!server.tools.length) {
      const discovered = condition(server, "ToolsDiscovered");
      return emptyState("No tools discovered", discovered ? discovered.message : "The Operator lists tools once the server is Ready.");
    }
    return h("section", { class: "panel" }, h("div", { class: "body flush" }, server.tools.map((tool) => h("div", { class: "tool" },
      h("div", { class: "tool-head" },
        h("span", { class: "tool-name", text: tool.name }),
        server.routed ? h("a", { class: "btn small", href: formatHash("playground", [server.route, tool.name]), "aria-label": "Try " + tool.name }, "Try it") : null),
      hintBadges(tool.annotations),
      tool.description ? h("p", { class: "tool-desc", text: tool.description }) : null,
      tool.inputSchema ? h("details", {}, h("summary", { text: "Input schema" }), codeBlock(pretty(tool.inputSchema))) : null))));
  }

  // ---------------------------------------------------------------- tools

  function renderTools(root, params) {
    const filters = { q: params.q || "" };
    const search = h("input", {
      type: "search", placeholder: "Search tools, descriptions, routes", "aria-label": "Search tools", value: filters.q,
      oninput: (e) => { filters.q = e.target.value; setParams(filters); draw(); },
    });
    const count = h("span", { class: "muted small" });
    const list = h("div", { class: "body flush" });
    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "Tools" }), h("p", { text: "Every tool the Operator discovered, across all servers." })),
        h("div", { class: "toolbar" }, search)),
      h("section", { class: "panel" }, h("header", {}, h("h2", { text: "Catalog" }), count), list));

    let lastKey = "";
    function draw() {
      const entries = searchCatalog(catalog(state.servers), filters.q);
      const all = catalog(state.servers).length;
      count.textContent = filters.q ? entries.length + " of " + all + " tools" : all + (all === 1 ? " tool" : " tools");
      const rows = entries.map((e) => h("tr", {},
        h("td", { class: "mono", text: e.tool }),
        h("td", {}, h("a", { class: "mono", href: formatHash("servers", [e.route, "tools"]), text: e.route })),
        h("td", {}, badge(e.state.label, e.state.cls)),
        h("td", { class: "wrap muted" }, e.description, hintBadges(e.annotations)),
        h("td", { class: "actions" }, e.playable ?
          h("a", { class: "btn small", href: formatHash("playground", [e.route, e.tool]), "aria-label": "Try " + e.tool + " on " + e.route }, "Try ▶") :
          [h("button", { type: "button", class: "btn small", disabled: true, "aria-describedby": "why-" + e.route + "-" + e.tool }, "Try ▶"),
            h("div", { class: "hint", id: "why-" + e.route + "-" + e.tool, text: e.reason })])));
      put(clear(list), state.serversLoaded ?
        table(["Tool", "Server", "State", "Description", ""], rows, filters.q ? "No tool matches “" + filters.q + "”." : "No tools discovered yet.") :
        emptyState("Loading…"));
    }

    draw();
    return (what) => {
      if (what !== "servers") return;
      const key = JSON.stringify(catalog(state.servers).map((e) => [e.tool, e.route, e.routed]));
      if (key !== lastKey) { lastKey = key; draw(); }
    };
  }

  // ---------------------------------------------------------------- traffic

  function renderTraffic(root, params) {
    const filters = { route: params.route || "", method: params.method || "", tool: params.tool || "", outcome: params.outcome || "" };
    let limit = TRAFFIC_PAGE;
    let toolbarKey = "";
    const toolbar = h("div", { class: "toolbar filters" });
    const pause = h("button", { type: "button", class: "btn", onclick: () => { state.trafficPaused = !state.trafficPaused; draw(); } });
    const clearBtn = h("button", { type: "button", class: "btn", onclick: () => { state.trafficHideBefore = state.seenTrafficId; draw(); } }, "Clear view");
    const summary = h("div");
    const tableBox = h("div");
    const filterNote = h("div");
    const scope = h("div");

    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "Traffic" }),
          h("p", { text: "MCP calls proxied by the Gateway, newest first. Metadata only: arguments and results are never recorded." })),
        h("div", { class: "toolbar" }, pause, clearBtn)),
      scope, toolbar, filterNote, outcomeHelp(), summary, tableBox);

    function setFilter(key, value) {
      filters[key] = value;
      limit = TRAFFIC_PAGE;
      setParams(filters);
      draw();
    }

    function draw() {
      put(clear(scope), scopeNote(trafficScope(state.trafficInstance)));
      pause.textContent = state.trafficPaused ? "▶ Resume" : "❚❚ Pause";
      pause.setAttribute("aria-pressed", String(state.trafficPaused));
      const visible = state.traffic.filter((r) => r.id > state.trafficHideBefore);
      const routes = [...new Set(distinct(visible, "route").concat(state.servers.filter((s) => s.routed).map((s) => s.route)))].sort();
      const opt = (values, all) => [{ value: "", label: all }].concat([...new Set(values)].sort().map((v) => ({ value: v, label: v })));
      // Keep a filter from the URL selectable even before matching traffic arrives.
      const keep = (values, v) => (v ? values.concat(v) : values);
      const options = {
        route: opt(keep(routes, filters.route), "All routes"),
        method: opt(keep(distinct(visible, "method"), filters.method), "All methods"),
        tool: opt(keep(distinct(visible, "tool"), filters.tool), "All tools"),
      };
      const key = JSON.stringify([options, filters]);
      if (key !== toolbarKey) {
        toolbarKey = key;
        put(clear(toolbar),
          select("Route", filters.route, options.route, (v) => setFilter("route", v)),
          select("Method", filters.method, options.method, (v) => setFilter("method", v)),
          select("Tool", filters.tool, options.tool, (v) => setFilter("tool", v)),
          select("Outcome", filters.outcome, [{ value: "", label: "All outcomes" }, { value: "errors", label: "Errors (not ok)" }]
            .concat(Object.keys(OUTCOMES).map((o) => ({ value: o, label: o }))), (v) => setFilter("outcome", v)));
      }

      const active = Object.entries(filters).filter(([, v]) => v);
      put(clear(filterNote), active.length ? h("p", { class: "filters-active" },
        h("span", { class: "muted", text: "Filtered by " + active.map(([k, v]) => k + " = " + v).join(", ") + ". " }),
        h("button", { type: "button", class: "linkish", onclick: () => { for (const k of Object.keys(filters)) filters[k] = ""; setParams(filters); draw(); } }, "Clear filters")) : null);

      const records = filterTraffic(visible, filters);
      put(clear(summary), records.length ? toolSummaryPanel(records, !filters.route) : null);
      put(clear(tableBox), trafficTable(records, {
        showRoute: true, limit, onMore: (n) => { limit = n; draw(); },
        empty: active.length ? "No recent requests match these filters." : undefined,
      }));
    }

    draw();
    return (what) => { if (what === "traffic" || what === "servers") draw(); };
  }

  // ---------------------------------------------------------------- theme

  function applyTheme(theme) {
    if (theme === "light" || theme === "dark") document.documentElement.dataset.theme = theme;
    else delete document.documentElement.dataset.theme;
  }

  // The theme is the only thing the console stores in the browser
  // (SPEC-0002-R13).
  function initTheme() {
    let theme = "";
    try { theme = localStorage.getItem(THEME_KEY) || ""; } catch { /* storage unavailable */ }
    applyTheme(theme);
    document.getElementById("theme-toggle").addEventListener("click", () => {
      const dark = document.documentElement.dataset.theme ? document.documentElement.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
      const next = dark ? "light" : "dark";
      applyTheme(next);
      try { localStorage.setItem(THEME_KEY, next); } catch { /* storage unavailable */ }
    });
  }

  // ---------------------------------------------------------------- start

  // Say what is not protected wherever the console is reachable from more
  // than this machine: without login, the console and MCP routes
  // (SPEC-0002-R12); with login, still the MCP routes (SPEC-0005-R16).
  function initNotice() {
    const el = document.getElementById("notice");
    if (isLoopbackHost(location.hostname)) {
      el.hidden = true;
      return;
    }
    const { title, text } = notice(state.auth);
    put(clear(el), h("strong", { text: title + " " }), text);
    el.hidden = false;
  }

  async function start() {
    initTheme();
    window.addEventListener("hashchange", render);
    window.addEventListener("pagehide", closePlayground);
    let resizeTimer = 0;
    window.addEventListener("resize", () => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => update("resize"), RESIZE_DELAY_MS);
    });
    render();
    loadVersion();
    await checkSession();
    render();
    createPoller(document, [
      { fn: refreshServers, ms: SERVER_POLL_MS },
      { fn: refreshStatus, ms: SERVER_POLL_MS },
      { fn: refreshTraffic, ms: TRAFFIC_POLL_MS },
      { fn: refreshAlerts, ms: SERVER_POLL_MS },
      { fn: refreshHistory, ms: HISTORY_POLL_MS },
    ]).start();
  }

  start();
})();
