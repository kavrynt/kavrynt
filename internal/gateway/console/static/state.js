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
    if (status === 200 && body && body.enabled === false) return { enabled: false, signedIn: true, username: "", expiresAt: "" };
    if (status === 200 && body && body.username) return { enabled: true, signedIn: true, username: String(body.username), expiresAt: String(body.expiresAt || "") };
    if (status === 401) return { enabled: true, signedIn: false, username: "", expiresAt: "" };
    return null;
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

  Object.assign(exports, { parseHash, formatHash, isLoopbackHost, createPoller, sessionState, loginMessage });
})(typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).state = {}));
