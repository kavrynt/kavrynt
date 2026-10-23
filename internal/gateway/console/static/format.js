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

  Object.assign(exports, { pretty, fmtBytes, fmtMs, fmtPct, ago, clock });
})(typeof module === "object" && module.exports ? module.exports : ((globalThis.Kavrynt = globalThis.Kavrynt || {}).format = {}));
