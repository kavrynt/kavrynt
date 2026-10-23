// Playground: calls MCP tools through the Gateway's own /mcp/ routes, as an
// agent would. Everything an MCP server returns is untrusted and is only
// inserted as text. Calls and their results live in memory only, never in
// browser storage (SPEC-0002-R13).
"use strict";

(() => {
  const { h, put, clear, toast, copy, badge, codeBlock, emptyState } = Kavrynt.dom;
  const { pretty, fmtMs } = Kavrynt.format;
  const { formatHash } = Kavrynt.state;

  const PROTOCOL_VERSION = "2025-06-18";
  const MAX_RAW_BYTES = 2 << 20;

  // Playground state survives page switches.
  const pg = {
    route: "",
    session: null,
    tools: [],
    selected: null,
    mode: "form",
    last: null,
    history: [],
  };

  function closePlayground() {
    if (pg.session) pg.session.close();
  }

  function gatewayURL(route) {
    return location.origin + "/mcp/" + route;
  }

  // McpSession speaks MCP Streamable HTTP to one Gateway route.
  class McpSession {
    constructor(routeName, clientVersion) {
      this.route = routeName;
      this.clientVersion = clientVersion;
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
        clientInfo: { name: "kavrynt-console", version: this.clientVersion || "dev" },
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

  // rpcError returns the JSON-RPC error object of a response, if any. The
  // Gateway's own errors ({"error": "..."}, with an HTTP error status) are not
  // JSON-RPC errors.
  function rpcError(message) {
    return message && message.error && typeof message.error === "object" ? message.error : null;
  }

  function describeFailure(result) {
    const error = rpcError(result.message);
    if (error) return "JSON-RPC error " + error.code + ": " + (error.message || "");
    if (result.status < 200 || result.status >= 300) {
      const detail = result.message && typeof result.message.error === "string" ? ": " + result.message.error : "";
      return (result.status ? "HTTP " + result.status : "Network error") + detail;
    }
    if (!result.message) return "The response was not JSON-RPC";
    return "";
  }

  // renderPlayground draws the Playground and returns its update function.
  // ctx: { state, refreshTraffic, rerender, isCurrent }.
  function renderPlayground(root, routeParam, toolParam, ctx) {
    const { state } = ctx;
    const routed = state.servers.filter((s) => s.routed);
    if (routeParam && routeParam !== pg.route) {
      if (pg.session) pg.session.close();
      Object.assign(pg, { route: routeParam, session: null, tools: [], selected: toolParam || null, last: null, history: [] });
    } else if (toolParam) {
      pg.selected = toolParam;
    }

    const serverSelect = h("select", { "aria-label": "MCP server", onchange: (e) => { location.hash = formatHash("playground", [e.target.value]); } },
      h("option", { value: "" }, routed.length ? "Choose an MCP server…" : "No routed servers"),
      routed.map((s) => h("option", { value: s.route }, s.route)));
    serverSelect.value = pg.route;

    const sessionBox = h("div", { class: "session-info", role: "status" });
    const toolsBox = h("div");
    const formBox = h("div");
    const resultBox = h("div");
    const workBox = h("div", {}, formBox, resultBox);
    const historyBox = h("div");

    put(root,
      h("div", { class: "page-head" },
        h("div", {}, h("h1", { text: "Playground" }),
          h("p", { text: "Call MCP tools through the Gateway, exactly as your agent would. Every call shows up under Traffic." })),
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
        s.initError ? badge("initialize failed", "bad") : badge("Session ready", "ok"),
        s.serverInfo ? badge((s.serverInfo.name || "server") + " " + (s.serverInfo.version || ""), "", "serverInfo reported by the MCP server") : null,
        s.protocolVersion ? badge("protocol " + s.protocolVersion) : null,
        s.initError ? null : s.id ? badge("session " + s.id.slice(0, 12) + (s.id.length > 12 ? "…" : "")) : badge("no session id"),
        h("span", { class: "mono muted", text: gatewayURL(pg.route) }),
        s.initError ? h("span", {}, h("span", { text: s.initError + ". Calls are sent without a session. " }),
          h("a", { href: formatHash("servers", [pg.route]), text: "Check the server's conditions" })) : null);
    }

    function drawTools() {
      clear(toolsBox);
      if (!pg.tools.length) {
        put(toolsBox, h("div", { class: "empty", style: "padding:20px" }, pg.session ? "No tools listed." : "…"));
        return;
      }
      put(toolsBox, pg.tools.map((tool) => h("button", {
        type: "button", class: "pg-tool" + (pg.mode === "form" && pg.selected === tool.name ? " active" : ""),
        "aria-pressed": String(pg.mode === "form" && pg.selected === tool.name),
        onclick: () => select(tool.name),
      }, h("span", { class: "tool-name", text: tool.name }), tool.description ? h("span", { class: "tool-desc", text: tool.description }) : null)));
    }

    function select(name) {
      pg.mode = "form";
      pg.selected = name;
      history.replaceState(null, "", formatHash("playground", [pg.route, name]));
      drawTools();
      drawWork();
    }

    function drawHistory() {
      clear(historyBox);
      if (!pg.history.length) {
        put(historyBox, h("div", { class: "muted", style: "padding:12px 14px", text: "Calls in this session appear here. They are kept in this page only." }));
        return;
      }
      put(historyBox, pg.history.map((item) => h("button", { type: "button", class: "history-item", onclick: () => { pg.last = item; drawResult(); } },
        h("span", { class: "mono", text: item.label }),
        h("span", {}, badge(item.result.status ? String(item.result.status) : "error", item.ok ? "ok" : "warn"), " ", h("span", { class: "muted", text: fmtMs(item.result.ms) })))));
    }

    // drawWork redraws the request form; drawResult only the response, so a
    // call keeps the arguments the user typed.
    function drawWork() {
      clear(formBox);
      if (pg.route) {
        if (pg.mode === "raw") put(formBox, rawPanel());
        else {
          const tool = pg.tools.find((t) => t.name === pg.selected);
          put(formBox, tool ? toolPanel(tool) : h("section", { class: "panel" }, emptyState("Choose a tool", "Pick a tool, or send any JSON-RPC request with Raw JSON-RPC.")));
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
      const fieldsBox = h("div");
      const toggle = h("button", { type: "button", class: "btn small" });
      const error = h("p", { class: "error-text", role: "alert" });

      function drawForm() {
        toggle.textContent = jsonMode ? "Use form" : "Edit as JSON";
        put(clear(fieldsBox), jsonMode || !fields.length ? (fields.length || jsonMode ? jsonArea : h("p", { class: "muted", text: "This tool takes no arguments." })) : fields.map((f) => f.el));
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
      }, fieldsBox, error, h("div", { class: "spaced", style: "margin-top:12px" }, toggle, callBtn));
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
      const method = h("input", { type: "text", id: "raw-method", value: "tools/list", style: "width:100%" });
      const params = h("textarea", { id: "raw-params", spellcheck: "false" });
      params.value = "{}";
      const error = h("p", { class: "error-text", role: "alert" });
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
        h("div", { class: "field" }, h("label", { for: "raw-method", text: "Method" }), method),
        h("div", { class: "field" }, h("label", { for: "raw-params", text: "Params" }), params),
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
      ctx.refreshTraffic();
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
      const session = new McpSession(routeName, state.version && state.version.version);
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
      if (ctx.isCurrent()) ctx.rerender();
      ctx.refreshTraffic();
    }

    drawHistory();
    connect(false);
    return (what) => {
      if (what !== "servers") return;
      const now = state.servers.filter((s) => s.routed).map((s) => s.route).join(",");
      if (now !== routed.map((s) => s.route).join(",")) ctx.rerender();
    };
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
    const error = rpcError(message);
    const meta = h("div", { class: "result-meta" },
      badge(result.status ? "HTTP " + result.status : "network error", result.status >= 200 && result.status < 300 ? "ok" : "bad"),
      badge(fmtMs(result.ms)),
      error ? badge("JSON-RPC error " + error.code, "bad") : null,
      rpcResult && rpcResult.isError ? badge("tool error", "warn") : null,
      item.ok ? badge("ok", "ok") : null);

    const tabs = ["Result", "Response", "Request"];
    let active = rpcResult && Array.isArray(rpcResult.content) ? "Result" : "Response";
    const tabBar = h("div", { class: "tabs", role: "tablist", style: "margin-bottom:10px" });
    const view = h("div");

    function draw() {
      put(clear(tabBar), tabs.map((t) => h("button", { type: "button", role: "tab", "aria-selected": String(t === active), class: t === active ? "active" : "", onclick: () => { active = t; draw(); } }, t)));
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

  Kavrynt.playground = { closePlayground, renderPlayground };
})();
