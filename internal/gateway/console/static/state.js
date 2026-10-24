// View state that lives outside the views: the URL hash, where the console is
// served from, and polling. Pure functions with no global DOM access (the
// poller takes its document and timers), so they run under Node's test
// runner (test/console) as well as in the browser.
"use strict";

// Shared by the browser (Kavrynt.state) and Node's test runner (module.exports).
((exports) => {
  // parseHash splits "#/page/a/b?key=value" into the page, its path segments,
  // and its query parameters (SPEC-0002-R10).
  function parseHash(hash) {
    const raw = String(hash || "").replace(/^#\/?/, "");
    const q = raw.indexOf("?");
    const path = q >= 0 ? raw.slice(0, q) : raw;
    const query = q >= 0 ? raw.slice(q + 1) : "";
    const [page, ...rest] = path.split("/").filter((s, i) => i === 0 || s !== "").map(safeDecode);
    const params = {};
    for (const [key, value] of new URLSearchParams(query)) params[key] = value;
    return { page: page || "", rest, params };
  }

  // formatHash is the inverse of parseHash. Empty parameters are left out, so
  // unfiltered views keep short URLs.
  function formatHash(page, rest = [], params = {}) {
    const path = [page, ...rest.filter((s) => s !== undefined && s !== null && s !== "")].map(encodeURIComponent).join("/");
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== null && value !== "") query.set(key, String(value));
    }
    const qs = query.toString();
    return "#/" + path + (qs ? "?" + qs : "");
  }

  function safeDecode(segment) {
    try { return decodeURIComponent(segment); } catch { return segment; }
  }

  // isLoopbackHost reports whether the console was loaded from this machine,
  // the only case in which the no-login notice is not shown (SPEC-0002-R12).
  function isLoopbackHost(hostname) {
    const host = String(hostname || "").toLowerCase().replace(/^\[|\]$/g, "");
    return host === "localhost" || host === "::1" || /^127(\.\d{1,3}){3}$/.test(host);
  }

  // createPoller runs tasks on their intervals while doc is visible, stops
  // while it is hidden, and runs every task once when it becomes visible again
  // (SPEC-0002-R14). Browsers reject window timers called as methods of another
  // object, so the defaults wrap them.
  const windowTimers = {
    setInterval: (fn, ms) => setInterval(fn, ms),
    clearInterval: (handle) => clearInterval(handle),
  };

  function createPoller(doc, tasks, timers = windowTimers) {
    let handles = [];
    const stop = () => {
      handles.forEach((handle) => timers.clearInterval(handle));
      handles = [];
    };
    const start = () => {
      stop();
      for (const task of tasks) {
        task.fn();
        handles.push(timers.setInterval(task.fn, task.ms));
      }
    };
    const onVisibility = () => { if (doc.hidden) stop(); else start(); };
    return {
      start() {
        doc.addEventListener("visibilitychange", onVisibility);
        if (!doc.hidden) start();
      },
      stop() {
        doc.removeEventListener("visibilitychange", onVisibility);
        stop();
      },
      get running() { return handles.length > 0; },
    };
  }

  // sessionState reads a GET /v1/session response (SPEC-0005-R07): login off,
  // signed in, or a login required.
  function sessionState(status, body) {
    // mcpAuth: whether /mcp/ routes need access tokens (SPEC-0006-R15).
    const mcpAuth = Boolean(body && body.mcpAuth === true);
    if (status === 200 && body && body.enabled === false) return { enabled: false, signedIn: true, username: "", expiresAt: "", mcpAuth };
    if (status === 200 && body && body.username) return { enabled: true, signedIn: true, username: String(body.username), expiresAt: String(body.expiresAt || ""), mcpAuth };
    if (status === 401) return { enabled: true, signedIn: false, username: "", expiresAt: "", mcpAuth };
    return null;
  }

  // notice is the banner shown when the console is opened from an address
  // other than loopback: whether MCP routes need tokens (SPEC-0006-R17) and
  // what the console login protects (SPEC-0005-R16).
  function notice(auth) {
    if (auth && auth.mcpAuth) {
      return {
        title: "MCP routes require access tokens.",
        text: "Agents need a token from a trusted authorization server for each route they call. " +
          (auth.enabled ? "This login protects the console only; it does not authorize MCP calls." : "This console has no login: anyone who can reach this Gateway can read its servers and traffic."),
      };
    }
    if (auth && auth.enabled) {
      return {
        title: "MCP routes are not authenticated.",
        text: "This login protects the console only. Anyone who can reach this Gateway can call every routed tool. Keep the Gateway Service internal or turn on MCP client authentication.",
      };
    }
    return {
      title: "No login.",
      text: "Anyone who can reach this Gateway can use this console and call every routed tool. Keep the Gateway Service internal or turn on MCP client authentication.",
    };
  }

  // authFailureMessage explains a Playground call that MCP client
  // authentication refused (SPEC-0006-R16), or returns null.
  function authFailureMessage(status) {
    switch (status) {
      case 401: return "The Gateway needs a valid access token for this route: one from a trusted authorization server, with this route's resource URI as its audience, not expired. Paste it above.";
      case 400: return "Send the access token in the Authorization header only; the Playground does this when you paste it above.";
      case 403: return "The access token is valid but lacks a scope the Gateway requires. Request a token with the required scopes.";
      case 503: return "The Gateway cannot load the signing keys of the token's authorization server right now. Try again in a minute.";
      default: return null;
    }
  }

  // loginMessage explains a failed POST /v1/session to the person signing in.
  function loginMessage(status, retryAfter) {
    switch (status) {
      case 401: return "Wrong username or password.";
      case 429: {
        const seconds = Number(retryAfter);
        return "Too many failed attempts. Try again in " + (seconds > 0 ? seconds + " s" : "a minute") + ".";
      }
      case 403: return "This Gateway does not accept logins from this address. Add it to --allowed-origins (Helm: gateway.config.allowedOrigins).";
      case 503: return "Login is not configured on this Gateway: the admin Secret is not mounted.";
      case 0: return "The Gateway did not answer. Check that it is running and reachable.";
      default: return "Login failed (HTTP " + status + ").";
    }
  }

  Object.assign(exports, { parseHash, formatHash, isLoopbackHost, createPoller, sessionState, loginMessage, authFailureMessage, notice });
})(typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).state = {}));
