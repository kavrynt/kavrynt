// Kavrynt Console: read-only views of the Gateway's JSON endpoints and a
// playground that calls MCP tools through the Gateway's own /mcp/ routes.
//
// Everything shown here that comes from MCP servers (tool names,
// descriptions, schemas, results) is untrusted. It is only ever inserted as
// text (textContent), never as HTML.
"use strict";

(() => {
  const PROTOCOL_VERSION = "2025-06-18";
  const SERVER_POLL_MS = 5000;
  const TRAFFIC_POLL_MS = 2000;
  const MAX_RAW_BYTES = 2 << 20;

  const state = {
    servers: [],
    serversLoaded: false,
    serversError: null,
    traffic: [],
    seenTrafficId: 0,
    freshIds: new Set(),
    trafficPaused: false,
    trafficHideBefore: 0,
    version: null,
    ready: null,
    filters: { search: "", namespace: "", status: "" },
    trafficFilters: { route: "", outcome: "" },
  };

  // Playground state survives page switches.
  const pg = {
    route: "",
    session: null,
    tools: [],
    selected: null,
    mode: "form",
    busy: false,
    last: null,
    history: [],
    log: [],
  };

  // ---------------------------------------------------------------- DOM

  function h(tag, props, ...children) {
    const el = document.createElement(tag);
    if (props) {
      for (const [key, value] of Object.entries(props)) {
        if (value === undefined || value === null || value === false) continue;
        if (key === "class") el.className = value;
        else if (key === "text") el.textContent = value;
        // The CSP forbids style attributes; CSSOM assignments are allowed.
        else if (key === "style") el.style.cssText = value;
        else if (key.startsWith("on") && typeof value === "function") el.addEventListener(key.slice(2), value);
        else if (key === "value") el.value = value;
        else if (key === "checked") el.checked = Boolean(value);
        else el.setAttribute(key, value === true ? "" : String(value));
      }
    }
    append(el, children);
    return el;
  }

  function append(el, children) {
    for (const child of children.flat(Infinity)) {
      if (child === null || child === undefined || child === false) continue;
      el.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
    return el;
  }

  // put appends children like append above: arrays are flattened and null,
  // undefined, and false skipped. Native Element.append would print them.
  function put(el, ...children) {
    return append(el, children);
  }

  function clear(el) {
    while (el.firstChild) el.removeChild(el.firstChild);
    return el;
  }

  const main = () => document.getElementById("main");

  function toast(message) {
    const el = document.getElementById("toast");
    el.textContent = message;
    el.classList.add("show");
    clearTimeout(toast.timer);
    toast.timer = setTimeout(() => el.classList.remove("show"), 1800);
  }

  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const area = h("textarea", { style: "position:fixed;opacity:0" });
      area.value = text;
      document.body.append(area);
      area.select();
      try { document.execCommand("copy"); } catch { /* ignore */ }
      area.remove();
    }
    toast("Copied to clipboard");
  }

  function copyRow(text) {
    return h("div", { class: "copy-row" },
      h("code", { text }),
      h("button", { class: "btn small", type: "button", onclick: () => copy(text) }, "Copy"));
  }

  function badge(text, cls) {
    return h("span", { class: "badge" + (cls ? " " + cls : "") }, text);
  }

  function codeBlock(text) {
    return h("pre", { class: "code" }, text);
  }

  function emptyState(title, body, code) {
    return h("div", { class: "empty" }, h("h3", { text: title }), body ? h("p", { text: body }) : null, code ? codeBlock(code) : null);
  }

  // ---------------------------------------------------------------- format

  function pretty(value) {
    try { return JSON.stringify(value, null, 2); } catch { return String(value); }
  }

  function fmtBytes(n) {
    if (!n) return "0 B";
    const units = ["B", "KiB", "MiB", "GiB"];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + " " + units[i];
  }

  function fmtMs(ms) {
    if (ms === null || ms === undefined || Number.isNaN(ms)) return "–";
    if (ms < 1) return ms.toFixed(2) + " ms";
    if (ms < 1000) return Math.round(ms) + " ms";
    return (ms / 1000).toFixed(2) + " s";
  }

  function ago(iso) {
    const t = Date.parse(iso);
    if (!t || t < 0) return "–";
    const s = Math.max(0, Math.round((Date.now() - t) / 1000));
    if (s < 60) return s + "s ago";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  }

  function clock(iso) {
    const d = new Date(iso);
    return isNaN(d) ? "–" : d.toLocaleTimeString([], { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
  }

  function percentile(values, p) {
    if (!values.length) return null;
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))];
  }

  // ---------------------------------------------------------------- data

  async function getJSON(path) {
    const res = await fetch(path, { headers: { Accept: "application/json" }, cache: "no-store" });
    if (!res.ok) throw new Error(path + " returned HTTP " + res.status);
    return res.json();
  }

  function condition(server, type) {
    return (server.conditions || []).find((c) => c.type === type) || null;
  }

  function serverState(server) {
    if (server.routed) return { cls: "ok", label: "Routed", detail: "The Gateway routes this server." };
    const ready = condition(server, "Ready");
    if (!ready) return { cls: "warn", label: "Pending", detail: "The Operator has not reported this server yet." };
    if (ready.status === "True") return { cls: "warn", label: "Syncing", detail: "Ready, waiting for the Gateway route." };
    const cls = ready.reason === "InvalidSpec" ? "bad" : "warn";
    const labels = { InvalidSpec: "Invalid spec", UnsupportedTransport: "Not proxied (" + server.transport + ")", NamespaceNotWatched: "Namespace not watched" };
    return { cls, label: labels[ready.reason] || ready.reason || "Not ready", detail: ready.message };
  }

  function toolsBadge(server) {
    const discovered = condition(server, "ToolsDiscovered");
    if (discovered && discovered.status === "True") return badge(server.tools.length + (server.tools.length === 1 ? " tool" : " tools"), "info");
    if (discovered && discovered.reason === "DiscoveryFailed") return badge("Tool discovery failed", "warn");
    return null;
  }

  function gatewayURL(route) {
    return location.origin + "/mcp/" + route;
  }

  async function refreshServers() {
    try {
      const data = await getJSON("/v1/servers");
      state.servers = data.servers || [];
      state.serversError = null;
    } catch (err) {
      state.serversError = err.message;
    }
    state.serversLoaded = true;
    if (current.update) current.update("servers");
  }

  async function refreshTraffic() {
    if (state.trafficPaused) return;
    try {
      const data = await getJSON("/v1/traffic");
      const records = data.requests || [];
      state.freshIds = new Set(records.filter((r) => r.id > state.seenTrafficId && state.seenTrafficId > 0).map((r) => r.id));
      if (records.length) state.seenTrafficId = Math.max(state.seenTrafficId, records[0].id);
      state.traffic = records;
    } catch { /* the status indicator reports Gateway problems */ }
    if (current.update) current.update("traffic");
  }

  async function refreshStatus() {
    const el = document.getElementById("gw-status");
    try {
      const res = await fetch("/readyz", { cache: "no-store" });
      const body = await res.json();
      state.ready = res.ok;
      put(clear(el), h("span", { class: "dot " + (res.ok ? "ok" : "bad") }),
        h("span", { text: res.ok ? "Gateway ready · " + body.routes + (body.routes === 1 ? " route" : " routes") : "Gateway not ready" }));
    } catch {
      state.ready = false;
      put(clear(el), h("span", { class: "dot bad" }), h("span", { text: "Gateway unreachable" }));
    }
  }

  async function loadVersion() {
    try {
      state.version = await getJSON("/version");
      document.getElementById("gw-version").textContent = "v" + (state.version.version || "dev");
    } catch { /* optional */ }
  }

  // ---------------------------------------------------------------- router

  let current = { name: "", update: null };

  function route() {
    const hash = location.hash.replace(/^#\/?/, "");
    const [page, ...rest] = hash.split("/").map(decodeURIComponent);
    return { page: page || "servers", rest };
  }

  function render() {
    const { page, rest } = route();
    document.querySelectorAll("[data-nav]").forEach((a) => a.classList.toggle("active", a.dataset.nav === page));
    const root = clear(main());
    current = { name: page, update: null };
    if (page === "servers" && rest[0]) renderServer(root, rest[0], rest[1] || "overview");
    else if (page === "playground") renderPlayground(root, rest[0], rest[1]);
    else if (page === "traffic") renderTraffic(root);
    else renderServers(root);
    root.focus({ preventScroll: true });
  }

  // ---------------------------------------------------------------- servers

  function renderServers(root) {
    const tiles = h("div", { class: "tiles" });
    const list = h("div");
    const nsSelect = h("select", { "aria-label": "Namespace", onchange: (e) => { state.filters.namespace = e.target.value; draw(); } });
    const search = h("input", {
      type: "search", placeholder: "Search servers or tools", "aria-label": "Search", value: state.filters.search,
      oninput: (e) => { state.filters.search = e.target.value; draw(); },
    });
    const statusSelect = h("select", { "aria-label": "Status", onchange: (e) => { state.filters.status = e.target.value; draw(); } },
      h("option", { value: "" }, "All statuses"),
      h("option", { value: "routed" }, "Routed"),
      h("option", { value: "not-routed" }, "Not routed"));
    statusSelect.value = state.filters.status;

    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "MCP servers" }), h("p", { text: "Every MCPServer the Gateway watches, its readiness, and the tools the Operator discovered." })),
        h("div", { class: "toolbar" }, search, nsSelect, statusSelect)),
      tiles, list);

    function drawTiles() {
      const servers = state.servers;
      const namespaces = new Set(servers.map((s) => s.namespace)).size;
      const routed = servers.filter((s) => s.routed).length;
      const tools = servers.reduce((n, s) => n + s.tools.length, 0);
      const errors = state.traffic.filter((r) => r.outcome !== "ok").length;
      put(clear(tiles),
        tile("Servers", servers.length, namespaces + (namespaces === 1 ? " namespace" : " namespaces")),
        tile("Routed", routed, servers.length - routed + " not routed"),
        tile("Tools", tools, "discovered by the Operator"),
        tile("Recent requests", state.traffic.length, state.traffic.length ? Math.round((errors / state.traffic.length) * 100) + "% errors" : "no traffic yet"));
    }

    function draw() {
      drawTiles();
      const servers = state.servers;
      const namespaces = [...new Set(servers.map((s) => s.namespace))].sort();
      put(clear(nsSelect), h("option", { value: "" }, "All namespaces"), namespaces.map((ns) => h("option", { value: ns }, ns)));
      nsSelect.value = namespaces.includes(state.filters.namespace) ? state.filters.namespace : "";

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
      const term = state.filters.search.trim().toLowerCase();
      const shown = servers.filter((s) => {
        if (state.filters.namespace && s.namespace !== state.filters.namespace) return false;
        if (state.filters.status === "routed" && !s.routed) return false;
        if (state.filters.status === "not-routed" && s.routed) return false;
        if (!term) return true;
        return s.route.toLowerCase().includes(term) || (s.description || "").toLowerCase().includes(term) ||
          s.tools.some((t) => t.name.toLowerCase().includes(term));
      });
      if (!shown.length) {
        put(list, emptyState("No servers match the filters"));
        return;
      }
      put(list, h("div", { class: "grid" }, shown.map(serverCard)));
    }

    current.update = (what) => { if (what === "servers") draw(); else drawTiles(); };
    draw();
  }

  function tile(label, value, sub) {
    return h("div", { class: "tile" }, h("div", { class: "label", text: label }), h("div", { class: "value", text: String(value) }), h("div", { class: "sub", text: sub }));
  }

  function serverCard(server) {
    const st = serverState(server);
    const open = () => { location.hash = "#/servers/" + encodeURIComponent(server.route); };
    return h("div", {
      class: "card clickable state-" + st.cls, tabindex: "0", role: "link", "aria-label": server.route,
      onclick: open, onkeydown: (e) => { if (e.key === "Enter") open(); },
    },
    h("div", { class: "card-head" },
      h("div", {}, h("h3", { text: server.name }), h("div", { class: "ns", text: server.namespace })),
      badge(st.label, st.cls)),
    server.description ? h("p", { class: "muted", style: "margin:8px 0 0", text: server.description }) : null,
    h("dl", {},
      h("dt", { text: "Transport" }), h("dd", { text: server.transport }),
      h("dt", { text: "Version" }), h("dd", { text: server.version }),
      h("dt", { text: "Endpoint" }), h("dd", { text: server.endpoint || "–", title: server.endpoint || "" }),
      h("dt", { text: "Tools" }), h("dd", {}, toolsBadge(server) || h("span", { class: "muted", text: "–" }))));
  }

  // ---------------------------------------------------------------- server detail

  function renderServer(root, routeName, tab) {
    const body = h("div");
    put(root, body);

    function draw() {
      const server = state.servers.find((s) => s.route === routeName);
      clear(body);
      if (!server) {
        put(body, h("div", { class: "crumbs" }, h("a", { href: "#/servers", text: "Servers" })),
          emptyState(state.serversLoaded ? "Server not found" : "Loading…", state.serversLoaded ? "No MCPServer is named " + routeName + "." : ""));
        return;
      }
      const st = serverState(server);
      put(body,
        h("div", { class: "crumbs" }, h("a", { href: "#/servers", text: "Servers" }), " / ", server.namespace, " / ", server.name),
        h("div", { class: "page-head" },
          h("div", {}, h("h1", { text: server.name }),
            h("div", { class: "badges", style: "margin-top:6px" }, badge(st.label, st.cls), toolsBadge(server), badge(server.transport), badge("v" + server.version))),
          h("div", { class: "toolbar" },
            server.routed ? h("a", { class: "btn primary", href: "#/playground/" + encodeURIComponent(server.route) }, "Open in playground") : null)),
        h("div", { class: "tabs", role: "tablist" },
          tabButton("overview", "Overview"), tabButton("tools", "Tools (" + server.tools.length + ")"), tabButton("traffic", "Traffic")));

      if (tab === "tools") put(body, toolsPanel(server));
      else if (tab === "traffic") put(body, trafficTable(state.traffic.filter((r) => r.route === server.route), false));
      else put(body, overview(server, st));
    }

    function tabButton(id, label) {
      return h("button", {
        type: "button", role: "tab", class: tab === id ? "active" : "", "aria-selected": String(tab === id),
        onclick: () => { location.hash = "#/servers/" + encodeURIComponent(routeName) + "/" + id; },
      }, label);
    }

    current.update = (what) => { if (what === "servers" || (what === "traffic" && tab === "traffic")) draw(); };
    draw();
  }

  function overview(server, st) {
    const url = gatewayURL(server.route);
    const conditions = h("table", {},
      h("thead", {}, h("tr", {}, ["Type", "Status", "Reason", "Message", "Changed"].map((c) => h("th", { text: c })))),
      h("tbody", {}, server.conditions.length ? server.conditions.map((c) => h("tr", {},
        h("td", { text: c.type }),
        h("td", {}, badge(c.status, c.status === "True" ? "ok" : c.status === "False" ? (c.type === "Ready" || c.type === "Accepted" ? "bad" : "warn") : "")),
        h("td", { text: c.reason }),
        h("td", { text: c.message }),
        h("td", { text: ago(c.lastTransitionTime), title: c.lastTransitionTime }))) :
        h("tr", {}, h("td", { colspan: "5", class: "muted", text: "The Operator has not reported conditions yet." }))));

    const summary = h("dl", { class: "kv" },
      h("dt", { text: "Namespace" }), h("dd", { text: server.namespace }),
      h("dt", { text: "Route" }), h("dd", { class: "mono", text: "/mcp/" + server.route }),
      h("dt", { text: "Endpoint" }), h("dd", { class: "mono", text: server.endpoint || "–", title: server.endpoint || "" }),
      h("dt", { text: "Created" }), h("dd", { text: ago(server.createdAt), title: server.createdAt }),
      server.description ? [h("dt", { text: "Description" }), h("dd", { text: server.description })] : null);

    const panels = [
      h("section", { class: "panel" }, h("header", {}, h("h2", { text: "Summary" }), badge(st.label, st.cls)),
        h("div", { class: "body stack" }, summary, st.detail ? h("p", { class: "muted", style: "margin:10px 0 0", text: st.detail }) : null)),
      h("section", { class: "panel" }, h("header", {}, h("h2", { text: "Conditions" })), h("div", { class: "body flush table-wrap" }, conditions)),
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
    const tabs = h("div", { class: "tabs", style: "margin-bottom:10px" });
    const out = h("div");
    function draw() {
      put(clear(tabs), names.map((n) => h("button", { type: "button", class: n === active ? "active" : "", onclick: () => { active = n; draw(); } }, n)));
      put(clear(out), codeBlock(snippets[active]),
        h("div", { style: "margin-top:8px" }, h("button", { class: "btn small", type: "button", onclick: () => copy(snippets[active]) }, "Copy snippet")));
    }
    draw();
    return h("section", { class: "panel" },
      h("header", {}, h("h2", { text: "Connect your agent" })),
      h("div", { class: "body stack" },
        h("p", { class: "muted", style: "margin:0", text: "Point any MCP client (Streamable HTTP) at this Gateway URL. Calls show up under Live traffic." }),
        copyRow(url), tabs, out,
        h("p", { class: "muted", style: "margin:0;font-size:12px", text: "The Gateway does not authenticate callers yet and never forwards Authorization or Cookie headers upstream." })));
  }

  function toolsPanel(server) {
    if (!server.tools.length) {
      const discovered = condition(server, "ToolsDiscovered");
      return emptyState("No tools discovered", discovered ? discovered.message : "The Operator lists tools once the server is Ready.");
    }
    return h("section", { class: "panel" }, h("div", { class: "body flush" }, server.tools.map((tool) => h("div", { class: "tool" },
      h("div", { class: "tool-head" },
        h("span", { class: "tool-name", text: tool.name }),
        server.routed ? h("a", { class: "btn small", href: "#/playground/" + encodeURIComponent(server.route) + "/" + encodeURIComponent(tool.name) }, "Try it") : null),
      tool.description ? h("p", { class: "tool-desc", text: tool.description }) : null,
      tool.inputSchema ? h("details", {}, h("summary", { text: "Input schema" }), codeBlock(pretty(tool.inputSchema))) : null))));
  }

  // ---------------------------------------------------------------- traffic

  function renderTraffic(root) {
    const routeSelect = h("select", { "aria-label": "Route", onchange: (e) => { state.trafficFilters.route = e.target.value; draw(); } });
    const outcomeSelect = h("select", { "aria-label": "Outcome", onchange: (e) => { state.trafficFilters.outcome = e.target.value; draw(); } },
      h("option", { value: "" }, "All outcomes"), h("option", { value: "ok" }, "OK"), h("option", { value: "errors" }, "Errors only"));
    outcomeSelect.value = state.trafficFilters.outcome;
    const pause = h("button", { type: "button", class: "btn", onclick: () => { state.trafficPaused = !state.trafficPaused; draw(); } });
    const clearBtn = h("button", { type: "button", class: "btn", onclick: () => { state.trafficHideBefore = state.seenTrafficId; draw(); } }, "Clear view");
    const summary = h("div");
    const table = h("div");

    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "Live traffic" }),
          h("p", { text: "MCP calls proxied by this Gateway replica, newest first. Metadata only: arguments and results are never recorded." })),
        h("div", { class: "toolbar" }, routeSelect, outcomeSelect, pause, clearBtn)),
      summary, table);

    function draw() {
      pause.textContent = state.trafficPaused ? "▶ Resume" : "❚❚ Pause";
      const routes = [...new Set(state.traffic.map((r) => r.route).concat(state.servers.filter((s) => s.routed).map((s) => s.route)))].sort();
      put(clear(routeSelect), h("option", { value: "" }, "All routes"), routes.map((r) => h("option", { value: r }, r)));
      routeSelect.value = routes.includes(state.trafficFilters.route) ? state.trafficFilters.route : "";

      const records = state.traffic.filter((r) => r.id > state.trafficHideBefore &&
        (!state.trafficFilters.route || r.route === state.trafficFilters.route) &&
        (!state.trafficFilters.outcome || (state.trafficFilters.outcome === "ok" ? r.outcome === "ok" : r.outcome !== "ok")));

      put(clear(summary), routeSummary(records));
      put(clear(table), trafficTable(records, true));
    }

    current.update = (what) => { if (what === "traffic") draw(); };
    draw();
  }

  function routeSummary(records) {
    if (!records.length) return null;
    const byRoute = new Map();
    for (const r of records) {
      if (!byRoute.has(r.route)) byRoute.set(r.route, []);
      byRoute.get(r.route).push(r);
    }
    const rows = [...byRoute.entries()].sort((a, b) => b[1].length - a[1].length).map(([routeName, rs]) => {
      const errors = rs.filter((r) => r.outcome !== "ok").length;
      const rate = errors / rs.length;
      const latencies = rs.map((r) => r.durationMs);
      return h("tr", {},
        h("td", { class: "mono", text: routeName }),
        h("td", { class: "num", text: String(rs.length) }),
        h("td", { class: "num", text: String(errors) }),
        h("td", {}, h("div", { class: "bar" + (rate > 0 ? " bad" : ""), title: Math.round(rate * 100) + "% errors" }, h("span", { style: "width:" + Math.max(rate * 100, rate > 0 ? 3 : 0) + "%" }))),
        h("td", { class: "num", text: fmtMs(percentile(latencies, 0.5)) }),
        h("td", { class: "num", text: fmtMs(percentile(latencies, 0.95)) }),
        h("td", { text: ago(rs[0].time) }));
    });
    return h("section", { class: "panel" }, h("header", {}, h("h2", { text: "By route" })),
      h("div", { class: "body flush table-wrap" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Route", "Requests", "Errors", "Error rate", "p50", "p95", "Last call"].map((c) => h("th", { text: c })))),
        h("tbody", {}, rows))));
  }

  const outcomeClass = { ok: "ok", tool_error: "warn", rpc_error: "warn", client_error: "warn" };

  function trafficTable(records, showRoute) {
    if (!records.length) {
      return emptyState("No traffic yet", "Call a tool from the Playground or point your agent at a Gateway route. Requests appear here within two seconds.");
    }
    return h("section", { class: "panel" }, h("div", { class: "body flush table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, ["Time", showRoute ? "Route" : null, "Method", "Tool", "Outcome", "HTTP", "Latency", "In", "Out"].filter(Boolean).map((c) => h("th", { text: c })))),
      h("tbody", {}, records.map((r) => h("tr", { class: state.freshIds.has(r.id) ? "fresh" : "" },
        h("td", { class: "mono", text: clock(r.time), title: r.time }),
        showRoute ? h("td", { class: "mono", text: r.route }) : null,
        h("td", { class: "mono", text: r.method }),
        h("td", { class: "mono", text: r.tool === "none" ? "–" : r.tool }),
        h("td", {}, badge(r.outcome, outcomeClass[r.outcome] || "bad")),
        h("td", { class: "num", text: String(r.status || "–") }),
        h("td", { class: "num", text: fmtMs(r.durationMs) }),
        h("td", { class: "num", text: fmtBytes(r.bytesIn) }),
        h("td", { class: "num", text: fmtBytes(r.bytesOut) })))))));
  }

  // ---------------------------------------------------------------- MCP client

  // McpSession speaks MCP Streamable HTTP to one Gateway route.
  class McpSession {
    constructor(routeName) {
      this.route = routeName;
      this.id = null;
      this.protocolVersion = null;
      this.seq = 0;
      this.serverInfo = null;
      this.capabilities = null;
      this.initError = null;
    }

    headers(method, params) {
      const headers = { "Content-Type": "application/json", Accept: "application/json, text/event-stream", "Mcp-Method": method };
      if (method === "tools/call" && params && /^[\x21-\x7e]{1,128}$/.test(params.name || "")) headers["Mcp-Name"] = params.name;
      if (this.id) headers["Mcp-Session-Id"] = this.id;
      if (this.protocolVersion) headers["MCP-Protocol-Version"] = this.protocolVersion;
      return headers;
    }

    async request(method, params) {
      const id = ++this.seq;
      const body = { jsonrpc: "2.0", id, method, params: params || {} };
      const started = performance.now();
      const res = await fetch("/mcp/" + this.route, { method: "POST", headers: this.headers(method, params), body: JSON.stringify(body), cache: "no-store" });
      const contentType = res.headers.get("Content-Type") || "";
      let message = null;
      let raw = "";
      if (contentType.includes("text/event-stream")) {
        ({ message, raw } = await readEventStream(res, id));
      } else {
        raw = (await res.text()).slice(0, MAX_RAW_BYTES);
        try { message = JSON.parse(raw); } catch { message = null; }
      }
      return { method, request: body, status: res.status, ms: performance.now() - started, message, raw, sessionId: res.headers.get("Mcp-Session-Id"), at: new Date() };
    }

    async notify(method) {
      try {
        const res = await fetch("/mcp/" + this.route, { method: "POST", headers: this.headers(method), body: JSON.stringify({ jsonrpc: "2.0", method }), cache: "no-store" });
        await res.body?.cancel();
      } catch { /* notifications are best effort */ }
    }

    async initialize() {
      const result = await this.request("initialize", {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: "kavrynt-console", version: (state.version && state.version.version) || "dev" },
      });
      const ok = result.status >= 200 && result.status < 300 && result.message && result.message.result;
      if (ok) {
        this.id = result.sessionId || null;
        this.protocolVersion = result.message.result.protocolVersion || PROTOCOL_VERSION;
        this.serverInfo = result.message.result.serverInfo || null;
        this.capabilities = result.message.result.capabilities || null;
        await this.notify("notifications/initialized");
      } else {
        this.initError = describeFailure(result);
      }
      return result;
    }

    close() {
      if (!this.id) return;
      fetch("/mcp/" + this.route, { method: "DELETE", headers: { "Mcp-Session-Id": this.id }, keepalive: true }).catch(() => {});
      this.id = null;
    }
  }

  // readEventStream reads SSE until the JSON-RPC response with id arrives.
  async function readEventStream(res, id) {
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let raw = "";
    let data = [];
    const dispatch = () => {
      if (!data.length) return null;
      let parsed = null;
      try { parsed = JSON.parse(data.join("\n")); } catch { parsed = null; }
      data = [];
      return parsed && parsed.id === id ? parsed : null;
    };
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      const chunk = decoder.decode(value, { stream: true });
      if (raw.length < MAX_RAW_BYTES) raw += chunk;
      buffer += chunk;
      let newline;
      while ((newline = buffer.search(/\r?\n/)) >= 0) {
        const line = buffer.slice(0, newline);
        buffer = buffer.slice(newline + (buffer[newline] === "\r" ? 2 : 1));
        if (line === "") {
          const message = dispatch();
          if (message) {
            reader.cancel().catch(() => {});
            return { message, raw };
          }
        } else if (line.startsWith("data:")) {
          data.push(line.slice(5).replace(/^ /, ""));
        }
      }
    }
    return { message: dispatch(), raw };
  }

  function describeFailure(result) {
    if (result.message && result.message.error) {
      return "JSON-RPC error " + result.message.error.code + ": " + (result.message.error.message || "");
    }
    if (result.status < 200 || result.status >= 300) {
      let detail = "";
      if (result.message && typeof result.message.error === "string") detail = ": " + result.message.error;
      return "HTTP " + result.status + detail;
    }
    if (!result.message) return "The response was not JSON-RPC";
    return "";
  }

  // ---------------------------------------------------------------- playground

  function renderPlayground(root, routeParam, toolParam) {
    const routed = state.servers.filter((s) => s.routed);
    if (routeParam && routeParam !== pg.route) {
      if (pg.session) pg.session.close();
      Object.assign(pg, { route: routeParam, session: null, tools: [], selected: toolParam || null, last: null, history: [] });
    } else if (toolParam) {
      pg.selected = toolParam;
    }

    const serverSelect = h("select", { "aria-label": "MCP server", onchange: (e) => { location.hash = "#/playground/" + encodeURIComponent(e.target.value); } },
      h("option", { value: "" }, routed.length ? "Choose an MCP server…" : "No routed servers"),
      routed.map((s) => h("option", { value: s.route }, s.route)));
    serverSelect.value = pg.route;

    const sessionBox = h("div", { class: "session-info" });
    const toolsBox = h("div");
    const formBox = h("div");
    const resultBox = h("div");
    const workBox = h("div", {}, formBox, resultBox);
    const historyBox = h("div");

    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "Playground" }),
          h("p", { text: "Call MCP tools through the Gateway, exactly as your agent would. Every call shows up under Live traffic." })),
        h("div", { class: "toolbar" }, serverSelect,
          h("button", { type: "button", class: "btn", disabled: !pg.route, onclick: () => connect(true) }, "Reconnect"))),
      h("section", { class: "panel" }, h("div", { class: "body" }, sessionBox)),
      h("div", { class: "pg" },
        h("div", {},
          h("section", { class: "panel" }, h("header", {}, h("h2", { text: "Tools" }),
            h("button", { type: "button", class: "btn small", onclick: () => { pg.mode = "raw"; pg.selected = null; drawTools(); drawWork(); } }, "Raw JSON-RPC")),
          h("div", { class: "body flush pg-tools" }, toolsBox)),
          h("section", { class: "panel" }, h("header", {}, h("h2", { text: "History" })), h("div", { class: "body flush" }, historyBox))),
        workBox));

    function drawSession() {
      clear(sessionBox);
      if (!pg.route) {
        put(sessionBox, h("span", { class: "muted", text: routed.length ? "Choose a routed MCP server to start a session." : "No server is routed yet. Register one and wait for it to become Ready." }));
        return;
      }
      const s = pg.session;
      if (!s) {
        put(sessionBox, badge("Connecting…"));
        return;
      }
      put(sessionBox,
        s.initError ? badge("initialize failed", "warn") : badge("Session ready", "ok"),
        s.serverInfo ? badge((s.serverInfo.name || "server") + " " + (s.serverInfo.version || "")) : null,
        s.protocolVersion ? badge("protocol " + s.protocolVersion) : null,
        s.id ? badge("session " + s.id.slice(0, 12) + (s.id.length > 12 ? "…" : "")) : badge("no session id"),
        h("span", { class: "mono muted", text: gatewayURL(pg.route) }),
        s.initError ? h("span", { class: "muted", text: s.initError + " — continuing without initialize." }) : null);
    }

    function drawTools() {
      clear(toolsBox);
      if (!pg.tools.length) {
        put(toolsBox, h("div", { class: "empty", style: "padding:20px" }, pg.session ? "No tools listed." : "…"));
        return;
      }
      put(toolsBox, pg.tools.map((tool) => h("div", {
        class: "pg-tool" + (pg.mode === "form" && pg.selected === tool.name ? " active" : ""), tabindex: "0",
        onclick: () => select(tool.name), onkeydown: (e) => { if (e.key === "Enter") select(tool.name); },
      }, h("div", { class: "tool-name", text: tool.name }), tool.description ? h("div", { class: "tool-desc", text: tool.description }) : null)));
    }

    function select(name) {
      pg.mode = "form";
      pg.selected = name;
      history.replaceState(null, "", "#/playground/" + encodeURIComponent(pg.route) + "/" + encodeURIComponent(name));
      drawTools();
      drawWork();
    }

    function drawHistory() {
      clear(historyBox);
      if (!pg.history.length) {
        put(historyBox, h("div", { class: "muted", style: "padding:12px 14px", text: "Calls in this session appear here." }));
        return;
      }
      put(historyBox, pg.history.map((item) => h("div", { class: "history-item", onclick: () => { pg.last = item; drawResult(); } },
        h("span", { class: "mono", text: item.label }),
        h("span", {}, badge(String(item.result.status), item.ok ? "ok" : "warn"), " ", h("span", { class: "muted", text: fmtMs(item.result.ms) })))));
    }

    // drawWork redraws the request form; drawResult only the response, so a
    // call keeps the arguments the user typed.
    function drawWork() {
      clear(formBox);
      if (pg.route) {
        if (pg.mode === "raw") put(formBox, rawPanel());
        else {
          const tool = pg.tools.find((t) => t.name === pg.selected);
          put(formBox, tool ? toolPanel(tool) : h("section", { class: "panel" }, emptyState("Choose a tool", "Pick a tool on the left, or send any JSON-RPC request with Raw JSON-RPC.")));
        }
      }
      drawResult();
    }

    function drawResult() {
      clear(resultBox);
      if (pg.route && pg.last) put(resultBox, resultPanel(pg.last));
    }

    function toolPanel(tool) {
      const schema = tool.inputSchema && typeof tool.inputSchema === "object" ? tool.inputSchema : {};
      const props = schema.properties && typeof schema.properties === "object" ? schema.properties : {};
      const required = Array.isArray(schema.required) ? schema.required : [];
      const fields = Object.entries(props).map(([name, prop]) => buildField(name, prop || {}, required.includes(name)));
      let jsonMode = fields.some((f) => f.complex) && fields.every((f) => f.complex);
      const jsonArea = h("textarea", { "aria-label": "Arguments JSON", spellcheck: "false" });
      jsonArea.value = pretty(skeleton(props));
      const formBox = h("div");
      const toggle = h("button", { type: "button", class: "btn small" });
      const error = h("p", { class: "muted", style: "color:var(--bad);margin:8px 0 0" });

      function drawForm() {
        toggle.textContent = jsonMode ? "Use form" : "Edit as JSON";
        put(clear(formBox), jsonMode || !fields.length ? (fields.length || jsonMode ? jsonArea : h("p", { class: "muted", text: "This tool takes no arguments." })) : fields.map((f) => f.el));
      }
      toggle.addEventListener("click", () => {
        try {
          if (jsonMode) {
            const parsed = JSON.parse(jsonArea.value || "{}");
            fields.forEach((f) => f.set(parsed[f.name]));
          } else {
            jsonArea.value = pretty(collect());
          }
          jsonMode = !jsonMode;
          error.textContent = "";
          drawForm();
        } catch (err) {
          error.textContent = err.message;
        }
      });

      function collect() {
        const args = {};
        for (const f of fields) {
          const value = f.get();
          if (value !== undefined) args[f.name] = value;
        }
        return args;
      }

      const callBtn = h("button", { type: "submit", class: "btn primary" }, "Call tool");
      const form = h("form", {
        onsubmit: async (e) => {
          e.preventDefault();
          error.textContent = "";
          let args;
          try {
            args = jsonMode ? JSON.parse(jsonArea.value || "{}") : collect();
          } catch (err) {
            error.textContent = "Arguments: " + err.message;
            return;
          }
          const missing = required.filter((name) => args[name] === undefined || args[name] === "");
          if (missing.length && !confirmMissing(missing)) return;
          callBtn.disabled = true;
          callBtn.textContent = "Calling…";
          await run("tools/call", { name: tool.name, arguments: args }, tool.name);
          callBtn.disabled = false;
          callBtn.textContent = "Call tool";
        },
      }, formBox, error, h("div", { class: "spaced", style: "margin-top:12px" }, toggle, callBtn));
      drawForm();

      return h("section", { class: "panel" },
        h("header", {}, h("h2", {}, h("span", { class: "tool-name", text: tool.name }))),
        h("div", { class: "body" },
          tool.description ? h("p", { class: "tool-desc", style: "margin:0 0 14px", text: tool.description }) : null,
          form,
          tool.inputSchema ? h("details", {}, h("summary", { text: "Input schema" }), codeBlock(pretty(tool.inputSchema))) : null));
    }

    function confirmMissing(missing) {
      // No blocking dialogs: warn once, call on the second click.
      if (pg.warned === missing.join(",")) return true;
      pg.warned = missing.join(",");
      toast("Missing required: " + missing.join(", ") + ". Click again to call anyway.");
      return false;
    }

    function rawPanel() {
      const method = h("input", { type: "text", value: "tools/list", "aria-label": "Method", style: "width:100%" });
      const params = h("textarea", { "aria-label": "Params JSON", spellcheck: "false" });
      params.value = "{}";
      const error = h("p", { class: "muted", style: "color:var(--bad);margin:8px 0 0" });
      return h("section", { class: "panel" },
        h("header", {}, h("h2", { text: "Raw JSON-RPC request" })),
        h("div", { class: "body" }, h("form", {
          onsubmit: async (e) => {
            e.preventDefault();
            let parsed;
            try { parsed = JSON.parse(params.value || "{}"); } catch (err) { error.textContent = "Params: " + err.message; return; }
            error.textContent = "";
            await run(method.value.trim(), parsed, method.value.trim());
          },
        },
        h("div", { class: "field" }, h("label", { text: "Method" }), method),
        h("div", { class: "field" }, h("label", { text: "Params" }), params),
        error,
        h("div", { style: "text-align:right" }, h("button", { type: "submit", class: "btn primary" }, "Send")))));
    }

    async function run(method, params, label) {
      if (!pg.session) return;
      let result;
      try {
        result = await pg.session.request(method, params);
      } catch (err) {
        result = { method, request: { method, params }, status: 0, ms: 0, message: null, raw: String(err), at: new Date() };
      }
      const ok = result.status >= 200 && result.status < 300 && result.message && !result.message.error && !(result.message.result && result.message.result.isError);
      const item = { label, result, ok };
      pg.history.unshift(item);
      pg.history = pg.history.slice(0, 25);
      pg.last = item;
      drawHistory();
      drawResult();
      refreshTraffic();
    }

    function drawAll() {
      drawSession();
      drawTools();
      drawWork();
      drawHistory();
    }

    async function connect(force) {
      if (!pg.route) {
        drawAll();
        return;
      }
      if (!force && (pg.session || pg.connecting === pg.route)) {
        drawAll();
        return;
      }
      if (pg.session) pg.session.close();
      const routeName = pg.route;
      pg.session = null;
      pg.tools = [];
      pg.connecting = routeName;
      drawAll();
      const session = new McpSession(routeName);
      try {
        await session.initialize();
      } catch (err) {
        session.initError = String(err);
      }
      let listed = null;
      try {
        listed = await session.request("tools/list", {});
      } catch { /* fall back to discovered tools */ }
      if (pg.route !== routeName) {
        session.close();
        return;
      }
      pg.connecting = null;
      pg.session = session;
      const tools = listed && listed.message && listed.message.result && Array.isArray(listed.message.result.tools) ? listed.message.result.tools : null;
      const server = state.servers.find((s) => s.route === routeName);
      pg.tools = (tools || (server ? server.tools : [])).filter((t) => t && typeof t.name === "string");
      if (!tools && listed) toast("tools/list failed (" + describeFailure(listed) + "); showing discovered tools");
      if (pg.selected && !pg.tools.some((t) => t.name === pg.selected)) pg.selected = null;
      // The page may have been redrawn while connecting; draw it again.
      if (route().page === "playground") render();
      refreshTraffic();
    }

    current.update = (what) => {
      if (what !== "servers") return;
      const now = state.servers.filter((s) => s.routed).map((s) => s.route).join(",");
      if (now !== routed.map((s) => s.route).join(",")) render();
    };
    drawHistory();
    connect(false);
  }

  function buildField(name, prop, required) {
    const type = Array.isArray(prop.type) ? prop.type.find((t) => t !== "null") : prop.type;
    const label = h("label", { for: "f-" + name }, name, required ? h("span", { class: "req", text: " *" }) : null,
      type ? h("span", { class: "hint", text: "  " + type }) : null);
    const hint = prop.description ? h("div", { class: "hint", text: prop.description }) : null;
    let input;
    let get;
    let set;
    let complex = false;
    if (Array.isArray(prop.enum)) {
      input = h("select", { id: "f-" + name }, required ? null : h("option", { value: "" }, "—"), prop.enum.map((v, i) => h("option", { value: String(i) }, JSON.stringify(v))));
      get = () => (input.value === "" ? undefined : prop.enum[Number(input.value)]);
      set = (v) => { const i = prop.enum.findIndex((e) => JSON.stringify(e) === JSON.stringify(v)); input.value = i >= 0 ? String(i) : ""; };
    } else if (type === "boolean") {
      input = h("select", { id: "f-" + name }, h("option", { value: "" }, "—"), h("option", { value: "true" }, "true"), h("option", { value: "false" }, "false"));
      get = () => (input.value === "" ? undefined : input.value === "true");
      set = (v) => { input.value = v === undefined ? "" : String(Boolean(v)); };
    } else if (type === "number" || type === "integer") {
      input = h("input", { id: "f-" + name, type: "number", step: type === "integer" ? "1" : "any" });
      get = () => (input.value === "" ? undefined : Number(input.value));
      set = (v) => { input.value = v === undefined ? "" : String(v); };
    } else if (type === "string" || !type) {
      input = h("input", { id: "f-" + name, type: "text", autocomplete: "off" });
      if (prop.default !== undefined) input.placeholder = String(prop.default);
      get = () => (input.value === "" ? undefined : input.value);
      set = (v) => { input.value = v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v); };
    } else {
      complex = true;
      input = h("textarea", { id: "f-" + name, spellcheck: "false", style: "min-height:80px" });
      input.value = type === "array" ? "[]" : "{}";
      get = () => { const text = input.value.trim(); return text === "" ? undefined : JSON.parse(text); };
      set = (v) => { input.value = v === undefined ? "" : pretty(v); };
    }
    if (prop.default !== undefined && !complex) set(prop.default);
    return { name, complex, get, set, el: h("div", { class: "field" }, label, input, hint) };
  }

  function skeleton(props) {
    const out = {};
    for (const [name, prop] of Object.entries(props)) {
      const type = Array.isArray(prop && prop.type) ? prop.type[0] : prop && prop.type;
      out[name] = prop && prop.default !== undefined ? prop.default :
        type === "number" || type === "integer" ? 0 : type === "boolean" ? false : type === "array" ? [] : type === "object" ? {} : "";
    }
    return out;
  }

  function resultPanel(item) {
    const { result } = item;
    const message = result.message;
    const rpcResult = message && message.result;
    const meta = h("div", { class: "result-meta" },
      badge(result.status ? "HTTP " + result.status : "network error", result.status >= 200 && result.status < 300 ? "ok" : "bad"),
      badge(fmtMs(result.ms)),
      message && message.error ? badge("JSON-RPC error " + message.error.code, "bad") : null,
      rpcResult && rpcResult.isError ? badge("tool error", "warn") : null,
      item.ok ? badge("ok", "ok") : null);

    const tabs = ["Result", "Response", "Request"];
    let active = rpcResult && Array.isArray(rpcResult.content) ? "Result" : "Response";
    const tabBar = h("div", { class: "tabs", style: "margin-bottom:10px" });
    const view = h("div");

    function draw() {
      put(clear(tabBar), tabs.map((t) => h("button", { type: "button", class: t === active ? "active" : "", onclick: () => { active = t; draw(); } }, t)));
      clear(view);
      if (active === "Request") put(view, codeBlock(pretty(result.request)));
      else if (active === "Response") put(view, codeBlock(message ? pretty(message) : result.raw || "(empty response)"));
      else put(view, contentBlocks(rpcResult, message));
    }
    draw();

    return h("section", { class: "panel" },
      h("header", {}, h("h2", { text: "Response · " + item.label }), h("button", { type: "button", class: "btn small", onclick: () => copy(message ? pretty(message) : result.raw) }, "Copy")),
      h("div", { class: "body" }, meta, tabBar, view));
  }

  function contentBlocks(rpcResult, message) {
    if (message && message.error) return codeBlock(pretty(message.error));
    if (!rpcResult) return h("p", { class: "muted", text: "No result." });
    const blocks = [];
    for (const block of Array.isArray(rpcResult.content) ? rpcResult.content : []) {
      if (block && block.type === "text") blocks.push(h("div", { class: "content-block", text: String(block.text) }));
      else if (block && block.type === "image" && /^image\/(png|jpeg|gif|webp)$/.test(block.mimeType || "") && /^[A-Za-z0-9+/=]+$/.test(block.data || "")) {
        blocks.push(h("div", { class: "content-block" }, h("img", { alt: "Tool image result", style: "max-width:100%", src: "data:" + block.mimeType + ";base64," + block.data })));
      } else blocks.push(codeBlock(pretty(block)));
    }
    if (rpcResult.structuredContent !== undefined) blocks.push(h("div", { class: "muted", style: "margin:6px 0 4px", text: "structuredContent" }), codeBlock(pretty(rpcResult.structuredContent)));
    if (!blocks.length) blocks.push(codeBlock(pretty(rpcResult)));
    return h("div", {}, blocks);
  }

  // ---------------------------------------------------------------- theme

  function applyTheme(theme) {
    if (theme === "light" || theme === "dark") document.documentElement.dataset.theme = theme;
    else delete document.documentElement.dataset.theme;
  }

  function initTheme() {
    let theme = "";
    try { theme = localStorage.getItem("kavrynt-theme") || ""; } catch { /* storage unavailable */ }
    applyTheme(theme);
    document.getElementById("theme-toggle").addEventListener("click", () => {
      const dark = document.documentElement.dataset.theme ? document.documentElement.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
      const next = dark ? "light" : "dark";
      applyTheme(next);
      try { localStorage.setItem("kavrynt-theme", next); } catch { /* storage unavailable */ }
    });
  }

  // ---------------------------------------------------------------- start

  initTheme();
  window.addEventListener("hashchange", render);
  window.addEventListener("pagehide", () => { if (pg.session) pg.session.close(); });
  render();
  loadVersion();
  refreshStatus();
  refreshServers();
  refreshTraffic();
  setInterval(refreshServers, SERVER_POLL_MS);
  setInterval(refreshStatus, SERVER_POLL_MS);
  setInterval(refreshTraffic, TRAFFIC_POLL_MS);
})();
