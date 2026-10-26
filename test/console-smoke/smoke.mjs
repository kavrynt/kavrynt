// Headless-browser smoke test of the Gateway console, run by
// scripts/e2e-kind.sh against a live Gateway. It covers what Node and Go
// unit tests cannot: rendering, keyboard use, layout width, and the login
// flow in a real browser.
//
// Environment: GATEWAY_URL (loopback), CONSOLE_PASSWORD, SERVER_ROUTE (a
// routed server with a tool), SERVER_TOOL.
import assert from "node:assert/strict";
import { chromium } from "playwright";

const gateway = new URL(required("GATEWAY_URL"));
const password = required("CONSOLE_PASSWORD");
const route = required("SERVER_ROUTE");
const tool = required("SERVER_TOOL");
const base = gateway.origin;
// A non-loopback name for the same Gateway, to see what remote users see.
const remoteHost = "kavrynt-smoke.test";

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(name + " is not set");
  return value;
}

const browser = await chromium.launch({ args: ["--host-resolver-rules=MAP " + remoteHost + " 127.0.0.1"] });
const errors = [];
let failed = false;

async function check(name, fn) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(name + ": " + e.message));
  try {
    await fn(page, context);
    console.log("ok   " + name);
  } catch (err) {
    failed = true;
    console.log("FAIL " + name + "\n     " + String(err.message || err).split("\n").join("\n     "));
  } finally {
    await context.close();
  }
}

async function signIn(page) {
  await page.goto(base + "/ui/");
  await page.fill("#login-password", password);
  await page.click("button:text('Sign in')");
  await page.waitForSelector("h1:text('Overview')");
}

// More than one page of traffic, including a tool call, for the Traffic view.
for (let i = 0; i < 105; i++) {
  const body = i === 0
    ? { jsonrpc: "2.0", id: i, method: "tools/call", params: { name: tool, arguments: { message: "smoke" } } }
    : { jsonrpc: "2.0", id: i, method: "tools/list", params: {} };
  const res = await fetch(base + "/mcp/" + route, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify(body),
  });
  await res.arrayBuffer();
}

// Verifies SPEC-0005-R14.
await check("login form, no data, wrong password, then sign-in", async (page) => {
  await page.goto(base + "/ui/#/servers");
  await page.waitForSelector("form.login");
  const text = await page.innerText("main");
  assert.ok(!text.includes(route), "server data shown before sign-in");
  await page.fill("#login-password", "not-the-password");
  await page.click("button:text('Sign in')");
  await page.waitForSelector("text=Wrong username or password.");
  await page.fill("#login-password", password);
  await page.click("button:text('Sign in')");
  await page.waitForSelector("h1:text('MCP servers')");
  await page.waitForSelector("[data-route='" + route + "']");
});

// Verifies SPEC-0005-R14.
await check("an ended session returns to the login form and drops data", async (page, context) => {
  await signIn(page);
  await page.goto(base + "/ui/#/servers");
  await page.waitForSelector("[data-route='" + route + "']");
  await context.clearCookies();
  await page.waitForSelector("form.login", { timeout: 15000 });
  assert.ok(!(await page.innerText("main")).includes(route), "server data left on screen after the session ended");
});

// Verifies SPEC-0005-R15.
await check("signed-in user, log out, and no stored password", async (page) => {
  await signIn(page);
  assert.equal(await page.innerText("#user .user-name"), "admin");
  const stored = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, cookie: document.cookie }));
  assert.ok(!stored.includes(password), "the password is in browser storage or a script-readable cookie");
  await page.click("#user button:text('Log out')");
  await page.waitForSelector("form.login");
  const res = await page.evaluate(async () => (await fetch("/v1/servers")).status);
  assert.equal(res, 401, "data endpoint still answers after logout");
});

// Verifies SPEC-0005-R16.
await check("the notice off loopback says MCP routes are not authenticated", async (page) => {
  await page.goto(gateway.protocol + "//" + remoteHost + ":" + gateway.port + "/ui/");
  await page.waitForSelector("#notice:not([hidden])");
  const notice = await page.innerText("#notice");
  assert.match(notice, /MCP routes are not authenticated/);
  assert.match(notice, /call every routed tool/);
  assert.equal(await page.isHidden("#notice"), false);
});

// Verifies SPEC-0004-R04.
await check("server cards show the version the server reports", async (page) => {
  await signIn(page);
  await page.goto(base + "/ui/#/servers");
  const card = page.locator("[data-route='" + route + "']");
  await card.waitFor();
  await page.waitForFunction((r) => document.querySelector("[data-route='" + r + "'] dd.version")?.textContent.includes("reported"), route, { timeout: 15000 });
  await page.goto(base + "/ui/#/servers/" + route);
  await page.waitForSelector("dt:text('Reported')");
  const reported = await page.locator("dt:text('Reported') + dd").innerText();
  assert.ok(!reported.startsWith("–"), "server detail shows no reported version: " + reported);
});

// Verifies SPEC-0002-R09 and SPEC-0008-R06: once traffic has loaded, the
// note names the Gateway replica that answered.
await check("every traffic view states its replica and window", async (page) => {
  await signIn(page);
  for (const hash of ["#/overview", "#/traffic", "#/servers/" + route + "/traffic"]) {
    await page.goto(base + "/ui/" + hash);
    await page.waitForSelector(".scope-note");
    await page.waitForFunction(() => /^Gateway replica \S+ only/.test(document.querySelector(".scope-note")?.innerText || ""));
    assert.match(await page.innerText(".scope-note"), /^Gateway replica kavrynt-gateway-\S+ only · last 500 requests/, hash);
  }
});

// Verifies SPEC-0002-R20.
await check("traffic shows 100 rows and offers the rest", async (page) => {
  await signIn(page);
  await page.goto(base + "/ui/#/traffic");
  const rows = page.locator("section:has(h2:text('Requests')) tbody tr");
  await page.waitForSelector(".more");
  assert.equal(await rows.count(), 100);
  await page.click("button:text('Show all')");
  assert.ok((await rows.count()) > 100, "Show all did not show the remaining requests");
});

// Verifies SPEC-0020-R06 and SPEC-0020-R07: the Alerts page renders its
// sections, the navigation links it from every page, and the Overview links
// to it.
await check("alerts: page, navigation, and overview link", async (page) => {
  await signIn(page);
  await page.waitForSelector("a[data-nav='alerts']");
  await page.click("a:has-text('Alerts →'), a:has-text('active alert')");
  await page.waitForSelector("h1:text('Alerts')");
  await page.waitForSelector("h2:text('Active')");
  await page.waitForSelector("h2:text('Resolved in the last hour')");
  for (const hash of ["#/servers", "#/traffic"]) {
    await page.goto(base + "/ui/" + hash);
    assert.equal(await page.isVisible("a[data-nav='alerts']"), true, hash + ": no Alerts navigation item");
  }
});

// Verifies SPEC-0024-R06, SPEC-0024-R07, SPEC-0024-R08, and SPEC-0024-R12:
// the Dashboard page and a server's Dashboard tab draw their charts with
// readable values and data tables, and keep range and scope in the URL.
await check("dashboards: page and server tab", async (page) => {
  await signIn(page);
  await page.click("a[data-nav='dashboard']");
  await page.waitForSelector("h1:text('Dashboard')");
  await page.waitForSelector("svg.chart");
  assert.ok((await page.locator("svg.chart").count()) >= 4, "the Dashboard page draws four charts");
  await page.waitForSelector("h2:text('Busiest servers')");
  await page.selectOption("select[aria-label='Range']", "6h");
  await page.waitForFunction(() => location.hash.includes("range=6h"));
  await page.selectOption("select[aria-label='Server']", route);
  await page.waitForFunction((r) => location.hash.includes("route=" + encodeURIComponent(r)), route);
  await page.waitForSelector("svg.chart");
  const chart = page.locator("svg.chart").first();
  await chart.focus();
  await page.keyboard.press("ArrowLeft");
  assert.match(await page.locator(".chart-readout").first().innerText(), /^\d\d:\d\d · ok /, "keyboard focus shows a point's values");
  assert.ok((await page.locator(".sr-only table tbody tr").count()) > 0, "no data table for screen readers");

  await page.goto(base + "/ui/#/servers/" + route + "/dashboard?range=1h");
  await page.waitForSelector("svg.chart");
  await page.waitForSelector("section:has(h2:text('Per tool')) a:text('" + tool + "')");
  await page.click("section:has(h2:text('Per tool')) a:text('" + tool + "')");
  await page.waitForFunction((t) => location.hash.includes("tool=" + t), tool);
  await page.waitForSelector("svg.chart");
  assert.equal(await page.locator("svg.chart").count(), 3, "a tool has no bytes chart");
});

// Verifies SPEC-0002-R15.
await check("keyboard: every control is named, reachable, and visibly focused", async (page) => {
  await signIn(page);
  for (const hash of ["#/overview", "#/dashboard", "#/servers", "#/tools", "#/traffic", "#/alerts", "#/servers/" + route, "#/servers/" + route + "/dashboard", "#/playground/" + route + "/" + tool]) {
    await page.goto(base + "/ui/" + hash);
    await page.waitForTimeout(1200);
    const unnamed = await page.evaluate(() => [...document.querySelectorAll("a[href], button, input, select, textarea, [tabindex]:not([tabindex='-1'])")]
      .filter((el) => el.offsetParent !== null)
      .filter((el) => !((el.getAttribute("aria-label") || "").trim() || (el.labels && el.labels.length) || el.textContent.trim() || el.getAttribute("title")))
      .map((el) => el.outerHTML.slice(0, 80)));
    assert.deepEqual(unnamed, [], hash + ": controls without an accessible name");
  }
  await page.goto(base + "/ui/#/servers");
  await page.waitForSelector("[data-route='" + route + "']");
  for (let i = 0; i < 30; i++) {
    await page.keyboard.press("Tab");
    const focus = await page.evaluate(() => {
      const el = document.activeElement;
      const cs = getComputedStyle(el);
      return { route: el.dataset ? el.dataset.route : undefined, outline: cs.outlineStyle !== "none" && cs.outlineWidth !== "0px", tag: el.tagName };
    });
    if (focus.tag === "BODY") continue;
    assert.ok(focus.outline, "focused " + focus.tag + " has no visible outline");
    if (focus.route === route) {
      await page.keyboard.press("Enter");
      await page.waitForSelector("h1:text('" + route.split(".").pop() + "')");
      return;
    }
  }
  assert.fail("the server card was not reachable with Tab");
});

// Verifies SPEC-0002-R17 and SPEC-0024-R12 (the dashboards at 360 px).
await check("every view fits 360 px without page scrolling", async (page) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await page.goto(base + "/ui/");
  await page.waitForSelector("form.login");
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), 0, "login");
  await signIn(page);
  for (const hash of ["#/overview", "#/dashboard?range=24h", "#/servers", "#/tools", "#/traffic", "#/alerts", "#/servers/" + route,
    "#/servers/" + route + "/traffic", "#/servers/" + route + "/dashboard", "#/servers/" + route + "/dashboard?tool=" + tool,
    "#/playground/" + route + "/" + tool]) {
    await page.goto(base + "/ui/" + hash);
    await page.waitForTimeout(1200);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    assert.equal(overflow, 0, hash + " scrolls horizontally by " + overflow + " px");
    assert.equal(await page.isVisible("#gw-status"), true, hash + ": Gateway status hidden");
  }
});

await browser.close();
if (errors.length) {
  failed = true;
  console.log("FAIL page errors:\n     " + errors.join("\n     "));
}
process.exit(failed ? 1 : 0);
