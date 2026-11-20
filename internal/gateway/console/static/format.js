// Display formatting. Pure functions with no DOM access, so they run under
// Node's test runner (test/console) as well as in the browser.
"use strict";

// Shared by the browser (Kavrynt.format) and Node's test runner (module.exports).
((exports) => {
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

  // fmtPct shows a 0..1 ratio as a percentage; small non-zero rates keep a
  // decimal so they never read as 0 %.
  function fmtPct(ratio) {
    if (ratio === null || ratio === undefined || Number.isNaN(ratio)) return "–";
    const pct = ratio * 100;
    if (pct > 0 && pct < 10) return pct.toFixed(1) + " %";
    return Math.round(pct) + " %";
  }

  function ago(iso, now = Date.now()) {
    const t = Date.parse(iso);
    if (!t || t < 0) return "–";
    const s = Math.max(0, Math.round((now - t) / 1000));
    if (s < 60) return s + "s ago";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  }

  function clock(iso) {
    const d = new Date(iso);
    return isNaN(d) ? "–" : d.toLocaleTimeString([], { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
  }

  // TOOL_HINTS are the MCP behavior hints the Operator records, in display
  // order, with their labels for true and false (SPEC-0028-R08).
  const TOOL_HINTS = [
    ["readOnlyHint", "read-only", "not read-only"],
    ["destructiveHint", "destructive", "not destructive"],
    ["idempotentHint", "idempotent", "not idempotent"],
    ["openWorldHint", "open world", "closed world"],
  ];

  // toolHints returns the labels of a tool's recorded hints; hints that are
  // absent or not booleans have none.
  function toolHints(annotations) {
    const labels = [];
    for (const [name, yes, no] of TOOL_HINTS) {
      const hint = annotations ? annotations[name] : undefined;
      if (hint === true) labels.push(yes);
      else if (hint === false) labels.push(no);
    }
    return labels;
  }

  // connectSnippets returns, by tab name, the ways to connect an agent to a
  // route. With opts.mcpAuth (the Gateway requires access tokens on /mcp/
  // routes, SPEC-0006), every snippet carries a placeholder for the bearer
  // token, never a real one.
  function connectSnippets(name, url, opts) {
    const auth = Boolean(opts && opts.mcpAuth);
    const client = { type: "http", url };
    if (auth) client.headers = { Authorization: "Bearer <access token>" };
    const python = [
      "# pip install \"mcp>=2\"",
      "import asyncio",
      "from mcp import ClientSession",
      "from mcp.client.streamable_http import streamable_http_client",
      "",
    ];
    if (auth) python.push("# The Gateway needs an access token: send \"Authorization: Bearer <access token>\"", "# with every request (for example through the HTTP client you pass in).", "");
    python.push(
      "async def main():",
      "    async with streamable_http_client(" + JSON.stringify(url) + ") as (read, write):",
      "        async with ClientSession(read, write) as session:",
      "            await session.initialize()",
      "            tools = await session.list_tools()",
      "            print([tool.name for tool in tools.tools])",
      "",
      "asyncio.run(main())",
    );
    return {
      "MCP client": pretty({ mcpServers: { [name]: client } }),
      "Python agent": python.join("\n"),
      curl: "curl -s -X POST '" + url + "' \\\n" + (auth ? "  -H \"Authorization: Bearer $ACCESS_TOKEN\" \\\n" : "") +
        "  -H 'Content-Type: application/json' \\\n  -H 'Accept: application/json, text/event-stream' \\\n  -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}'",
    };
  }

  // connectNote is the sentence under the connection snippets: what the
  // Gateway requires of callers, as the Gateway itself reports it, and what
  // it never forwards upstream.
  function connectNote(mcpAuth) {
    return (mcpAuth ?
      "This Gateway requires an access token from a trusted authorization server, with this route's resource URI as its audience. " :
      "This Gateway does not require access tokens on its MCP routes: anyone who can reach it can call this server's tools. ") +
      "It never forwards Authorization or Cookie headers upstream.";
  }

  Object.assign(exports, { pretty, fmtBytes, fmtMs, fmtPct, ago, clock, toolHints, connectSnippets, connectNote });
})(typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).format = {}));
