// Kavrynt Console: read-only views of the Gateway's JSON endpoints
// (/v1/servers, /v1/traffic, /readyz, /version) and a playground that calls
// MCP tools through the Gateway's own /mcp/ routes.
//
// Everything shown here that comes from MCP servers (tool names,
// descriptions, schemas, results) is untrusted. It is only ever inserted as
// text (textContent), never as HTML.
"use strict";

(() => {
  const { h, put, clear, toast, copy, copyRow, badge, codeBlock, emptyState, tile, panel, scopeNote } = Kavrynt.dom;
  const { pretty, fmtBytes, fmtMs, fmtPct, ago, clock } = Kavrynt.format;
  const {
    scopeNote: trafficScope, OUTCOMES, outcomeClass, condition, serverHealth, versionInfo, attention, overview, failingTools, toolSummary,
    filterTraffic, filterServers, distinct, catalog, searchCatalog, diagnostics, stripQuery,
  } = Kavrynt.stats;
  const { parseHash, formatHash, isLoopbackHost, createPoller, sessionState, loginMessage } = Kavrynt.state;
  const { renderPlayground, closePlayground, resetPlayground } = Kavrynt.playground;

  const SERVER_POLL_MS = 5000;
  const TRAFFIC_POLL_MS = 2000;
  const TRAFFIC_PAGE = 100;
  const THEME_KEY = "kavrynt-theme";

  const state = {
    servers: [],
    serversLoaded: false,
    serversError: null,
    traffic: [],
    // Pod name of the Gateway replica whose recent traffic is shown.
    trafficInstance: "",
    seenTrafficId: 0,
    freshIds: new Set(),
    trafficPaused: false,
    trafficHideBefore: 0,
    version: null,
    ready: null,
    routes: null,
    // Console admin login (SPEC-0005). checked is false until GET
    // /v1/session answers; nothing is drawn or fetched before that.
    auth: { checked: false, enabled: false, signedIn: false, username: "", expiresAt: "" },
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
    Object.assign(state, { servers: [], serversLoaded: false, serversError: null, traffic: [], seenTrafficId: 0, freshIds: new Set() });
    resetPlayground();
    drawUser();
    render();
    if (wasSignedIn && message) toast(message);
  }

  async function signedIn() {
    await checkSession();
    render();
    refreshServers();
    refreshTraffic();
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
    servers: (root, rest, params) => (rest[0] ? renderServer(root, rest[0], rest[1] || "overview") : renderServers(root, params)),
    tools: (root, rest, params) => renderTools(root, params),
    playground: (root, rest) => renderPlayground(root, rest[0], rest[1], {
      state, refreshTraffic, rerender: render, isCurrent: () => parseHash(location.hash).page === "playground",
    }),
    traffic: (root, rest, params) => renderTraffic(root, params),
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

      put(body,
        panel("Needs attention", attentionBody, items.length ? badge(String(items.length), "warn") : badge("none", "ok")),
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

  function renderServer(root, routeName, tab) {
    const body = h("div");
    put(root, body);
    let limit = TRAFFIC_PAGE;

    function draw() {
      const server = state.servers.find((s) => s.route === routeName);
      clear(body);
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
          tabButton("overview", "Overview"), tabButton("tools", "Tools (" + server.tools.length + ")"), tabButton("traffic", "Traffic (" + health.stats.calls + ")")));

      if (tab === "tools") put(body, toolsPanel(server));
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
    return (what) => { if (what === "servers" || (what === "traffic" && tab === "traffic")) draw(); };
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
    const snippets = {
      "MCP client": pretty({ mcpServers: { [server.name]: { type: "http", url } } }),
      "Python agent": [
        "# pip install mcp",
        "import asyncio",
        "from mcp import ClientSession",
        "from mcp.client.streamable_http import streamablehttp_client",
        "",
        "async def main():",
        "    async with streamablehttp_client(\"" + url + "\") as (read, write, _):",
        "        async with ClientSession(read, write) as session:",
        "            await session.initialize()",
        "            tools = await session.list_tools()",
        "            print([tool.name for tool in tools.tools])",
        "",
        "asyncio.run(main())",
      ].join("\n"),
      curl: "curl -s -X POST '" + url + "' \\\n  -H 'Content-Type: application/json' \\\n  -H 'Accept: application/json, text/event-stream' \\\n  -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}'",
    };
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

  function toolsPanel(server) {
    if (!server.tools.length) {
      const discovered = condition(server, "ToolsDiscovered");
      return emptyState("No tools discovered", discovered ? discovered.message : "The Operator lists tools once the server is Ready.");
    }
    return h("section", { class: "panel" }, h("div", { class: "body flush" }, server.tools.map((tool) => h("div", { class: "tool" },
      h("div", { class: "tool-head" },
        h("span", { class: "tool-name", text: tool.name }),
        server.routed ? h("a", { class: "btn small", href: formatHash("playground", [server.route, tool.name]), "aria-label": "Try " + tool.name }, "Try it") : null),
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
        h("td", { class: "wrap muted", text: e.description }),
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
    if (state.auth.enabled) {
      put(clear(el), h("strong", { text: "MCP routes are not authenticated. " }),
        "This login protects the console only. Anyone who can reach this Gateway can call every routed tool. Keep the Gateway Service internal until client authentication is available.");
    } else {
      put(clear(el), h("strong", { text: "No login. " }),
        "Anyone who can reach this Gateway can use this console and call every routed tool. Keep the Gateway Service internal until client authentication is available.");
    }
    el.hidden = false;
  }

  async function start() {
    initTheme();
    window.addEventListener("hashchange", render);
    window.addEventListener("pagehide", closePlayground);
    render();
    loadVersion();
    await checkSession();
    render();
    createPoller(document, [
      { fn: refreshServers, ms: SERVER_POLL_MS },
      { fn: refreshStatus, ms: SERVER_POLL_MS },
      { fn: refreshTraffic, ms: TRAFFIC_POLL_MS },
    ]).start();
  }

  start();
})();
