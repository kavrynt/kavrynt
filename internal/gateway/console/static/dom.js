// DOM helpers. Untrusted data is only ever inserted as text: h() sets
// textContent and creates text nodes, never HTML.
"use strict";

(() => {
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

  const SVG_NS = "http://www.w3.org/2000/svg";

  // svg creates an SVG element like h() creates HTML ones: attributes are
  // set one by one, listeners added, and children are elements or text
  // nodes, never markup.
  function svg(tag, attrs, ...children) {
    const el = document.createElementNS(SVG_NS, tag);
    for (const [key, value] of Object.entries(attrs || {})) {
      if (value === undefined || value === null || value === false) continue;
      if (key.startsWith("on") && typeof value === "function") el.addEventListener(key.slice(2), value);
      else el.setAttribute(key, String(value));
    }
    return append(el, children);
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

  function toast(message) {
    const el = document.getElementById("toast");
    el.textContent = message;
    el.classList.add("show");
    clearTimeout(toast.timer);
    toast.timer = setTimeout(() => el.classList.remove("show"), 2400);
  }

  async function copy(text, done = "Copied to clipboard") {
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
    toast(done);
  }

  function copyRow(text) {
    return h("div", { class: "copy-row" },
      h("code", { text }),
      h("button", { class: "btn small", type: "button", "aria-label": "Copy " + text, onclick: () => copy(text) }, "Copy"));
  }

  function badge(text, cls, title) {
    return h("span", { class: "badge" + (cls ? " " + cls : ""), title }, text);
  }

  function codeBlock(text) {
    return h("pre", { class: "code", tabindex: "0" }, text);
  }

  function emptyState(title, body, code) {
    return h("div", { class: "empty" }, h("h3", { text: title }), body ? h("p", { text: body }) : null, code ? codeBlock(code) : null);
  }

  function tile(label, value, sub, cls) {
    return h("div", { class: "tile" + (cls ? " " + cls : "") },
      h("div", { class: "label", text: label }), h("div", { class: "value", text: String(value) }), h("div", { class: "sub", text: sub }));
  }

  function panel(title, body, extra) {
    return h("section", { class: "panel" }, h("header", {}, h("h2", { text: title }), extra || null), body);
  }

  // ICONS are the console's own line icons (24 x 24, drawn with the text
  // colour by CSS), so they look the same on every system, unlike the
  // Unicode symbols they replace.
  const ICONS = {
    overview: [["circle", { cx: 12, cy: 12, r: 9 }], ["circle", { cx: 12, cy: 12, r: 3 }]],
    dashboard: [["path", { d: "M5 20v-9M12 20V4M19 20v-6" }]],
    servers: [["rect", { x: 3, y: 4, width: 18, height: 6, rx: 1.5 }], ["rect", { x: 3, y: 14, width: 18, height: 6, rx: 1.5 }], ["path", { d: "M7 7h.01M7 17h.01" }]],
    tools: [["path", { d: "M4 7h10M18 7h2M4 17h2M10 17h10" }], ["circle", { cx: 16, cy: 7, r: 2 }], ["circle", { cx: 8, cy: 17, r: 2 }]],
    playground: [["path", { d: "M8 5l11 7-11 7z" }]],
    traffic: [["path", { d: "M3 12h4l3-8 4 16 3-8h4" }]],
    alerts: [["path", { d: "M6 17v-6a6 6 0 0 1 12 0v6l2 2H4z" }], ["path", { d: "M10 21h4" }]],
    light: [["circle", { cx: 12, cy: 12, r: 4 }], ["path", { d: "M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M5 19l1.5-1.5M17.5 6.5L19 5" }]],
    dark: [["path", { d: "M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z" }]],
    system: [["rect", { x: 3, y: 4, width: 18, height: 12, rx: 2 }], ["path", { d: "M8 20h8M12 16v4" }]],
  };

  // icon returns the named icon as a decorative SVG, or null.
  function icon(name) {
    const parts = ICONS[name];
    if (!parts) return null;
    return svg("svg", { viewBox: "0 0 24 24", width: 18, height: 18, "aria-hidden": "true", focusable: "false", class: "icon" },
      parts.map(([tag, attrs]) => svg(tag, attrs)));
  }

  function scopeNote(text) {
    return h("p", { class: "scope-note", text });
  }

  Kavrynt.dom = { h, svg, icon, put, clear, toast, copy, copyRow, badge, codeBlock, emptyState, tile, panel, scopeNote };
})();
