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

  function scopeNote(text) {
    return h("p", { class: "scope-note", text });
  }

  Kavrynt.dom = { h, svg, put, clear, toast, copy, copyRow, badge, codeBlock, emptyState, tile, panel, scopeNote };
})();
