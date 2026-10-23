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

  Object.assign(exports, { parseHash, formatHash, isLoopbackHost, createPoller });
})(typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).state = {}));
