# SPEC-0002 plan: Console v2: operations UI

The design for the `Accepted` spec. Sections marked "as built" record where
the implementation settled a choice the proposal left open.

## Information architecture

```text
Kavrynt console
├── Overview      #/overview           new, default landing
├── Servers       #/servers?ns=&state=&q=
│   └── <route>   #/servers/<route>/{overview|tools|traffic|conditions}
├── Tools         #/tools?q=           new, catalog across servers
├── Playground    #/playground/<route>/<tool>
└── Traffic       #/traffic?route=&method=&tool=&outcome=
```

Hash routes keep working without server-side routing, and the query part
after `?` inside the hash carries filters (R10). Unknown keys are ignored, so
old links stay valid.

## Wireframes

Overview (R01, R02, R03, R09, R12):

```text
┌──────────┬───────────────────────────────────────────────────────────────┐
│ Kavrynt  │ ⚠ No login: anyone who can reach this Gateway can use this     │
│          │   console and call its routes.                    Learn more → │
│ Overview │───────────────────────────────────────────────────────────────│
│ Servers  │ Overview                            Gateway ready · 0.0.x │
│ Tools    │ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌───────────────────┐  │
│ Playgr.  │ │ Servers  │ │ Routed   │ │ Tools    │ │ Requests  Errors  │  │
│ Traffic  │ │    12    │ │ 10 of 12 │ │    57    │ │  412      3.4 %   │  │
│          │ └──────────┘ └──────────┘ └──────────┘ │  p95 182 ms       │  │
│          │                                        └───────────────────┘  │
│          │ Needs attention                                                │
│          │  ● team-a.docs     Ready=False    EndpointRejected   "…"   →   │
│          │  ● ops.runbooks    ToolsDiscovered=False  Timeout     "…"   →   │
│          │                                                                │
│          │ Failing tools                     this replica · last 500 reqs │
│          │  search     team-a.search   18 % errors   p95 1.2 s        →   │
│          │  get_issue  eng.tracker      4 % errors   p95 240 ms       →   │
└──────────┴───────────────────────────────────────────────────────────────┘
```

Tools catalog (R04, R05):

```text
 Tools                                   [ Search tools, descriptions…   ]
 ┌───────────────┬──────────────────┬─────────┬──────────────────────────┐
 │ Tool          │ Server           │ State   │ Description              │
 ├───────────────┼──────────────────┼─────────┼──────────────────────────┤
 │ search        │ team-a.search    │ Routed  │ Full-text search over …  │ [Try ▶]
 │ search_issues │ eng.tracker      │ Routed  │ Search issues by …       │ [Try ▶]
 │ summarize     │ team-b.notes     │ Not rt. │ Summarize a note         │ [Try ▶] disabled: not routed
 └───────────────┴──────────────────┴─────────┴──────────────────────────┘
```

Traffic (R06, R07, R08, R09):

```text
 Traffic        this replica · last 500 requests              [❚❚ Pause]
 [Route ▾] [Method ▾] [Tool ▾] [Outcome ▾]                 Outcomes (?)
 Per tool
  tool        calls  errors  p50     p95
  search        120   18 %   210 ms  1.2 s
  echo           88    0 %    12 ms   30 ms
 Requests
  12:01:07  team-a.search  tools/call  search  upstream_unreachable  502  1.0 s
  …
```

At 360 px the sidebar collapses to a top bar with a menu button, tiles stack
in one column, and tables scroll inside their panel (R17).

## Design

- **Modules (as built).** `app.js` is split into files with no bundler. They
  are classic deferred scripts sharing one `Kavrynt` namespace, not ES
  modules: browsers fetch module scripts with an `Origin` header even from
  the same origin, and the Gateway rejects origins outside
  `--allowed-origins`, so behind an Ingress without that setting the console
  loaded blank. The pure files also assign to `module.exports`, so Node's
  test runner loads them directly. A Go test keeps `index.html` free of
  module scripts and in dependency order:
  - `stats.js` (pure): outcome definitions, server state and health,
    attention list, per-tool summary, failing tools, filters, catalog,
    diagnostics.
  - `state.js` (pure): URL hash parsing and formatting, loopback host check,
    visibility-aware poller with injectable document and timers.
  - `format.js` (pure): number, size, time formatting.
  - `dom.js`: `h`, `put`, `clear`, and small text-only building blocks.
  - `playground.js`: MCP session, schema forms, results.
  - `app.js`: router, polling, theme, and the Overview, Servers, server
    detail, Tools, and Traffic views. Views stay in one module: they share
    helpers and are each small.
- **Pure logic first.** Everything R01–R11 compute lives in `stats.js` and
  `state.js` as functions of `/v1/servers` and `/v1/traffic` JSON, so it can
  be tested without a browser.
- **Outcome definitions** (R08) are one table in `stats.js`, copied from the
  Gateway component doc. A Go test checks that every outcome constant in
  `internal/gateway/api/traffic.go` has a definition, so the two cannot drift.
- **Polling** (R14) listens to `visibilitychange`; hidden pages clear their
  timers.
- **Redraws keep focus (as built).** Views rebuild their content on each
  poll; the update dispatcher moves keyboard focus back to the control with
  the same link, label, or id. The Traffic filters are rebuilt only when
  their options change, so an open dropdown is not closed by a poll.
- **Server health (R19, as built).** A routed server needs attention when
  `ToolsDiscovered` is `False` or at least half of at least five recent
  requests failed. The thresholds are constants in `stats.js`. Cards show
  the recent request count and error rate; the detail view shows a callout.
- **Traffic paging (R20, as built).** 100 rows, then "Show 100 more" and
  "Show all". Filters reset the page.
- **Storage** (R13): only the theme preference is stored. A Go test scans the
  static files and fails on any `localStorage`, `sessionStorage`,
  `indexedDB`, or `document.cookie` use outside the theme helper.
- **Diagnostics** (R11) are built from already-fetched JSON; the endpoint is
  shown without its query string.
- **Size budget** (R18): a Go test sums the embedded files.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/console/static/index.html` | Overview and Tools nav, module script, notice slot, skip link |
| `internal/gateway/console/static/*.js` | Split into modules; new Overview and Tools views |
| `internal/gateway/console/static/app.css` | Notice, health states, narrow layout, focus styles, AA tokens |
| `internal/gateway/console/console_test.go` | Storage scan, size and self-containment budget, module media type |
| `internal/gateway/console/contrast_test.go` | WCAG AA contrast of the theme tokens in both themes |
| `internal/gateway/api/outcomes_test.go` | Outcome definitions match outcome constants and the Gateway doc |
| `test/console/*.test.mjs` | Node tests for the pure modules (`make console-test`) |
| `Makefile`, `.github/workflows/qa.yml` | `console-test` in `make qa` and the Go QA job (Node 22) |
| `scripts/e2e-kind.sh` | Headless smoke test of each view (not decided) |
| `docs/components/gateway/README.md` | Console section, when `Implemented` |

The Gateway's Go code and its endpoints do not change.

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0002-R01, R02, R03 | `stats.test.mjs` overview, attention, and failing-tools cases |
| SPEC-0002-R04, R05 | `stats.test.mjs` catalog, search, and playability |
| SPEC-0002-R06, R07 | `stats.test.mjs` filters and per-tool summary |
| SPEC-0002-R08 | `outcomes_test.go` every outcome constant is defined in `stats.js` and the Gateway doc; `stats.test.mjs` definitions are non-empty |
| SPEC-0002-R09 | `test/console-smoke/smoke.mjs` scope note on Overview, Traffic, and server traffic |
| SPEC-0002-R10 | `state.test.mjs` hash round trip |
| SPEC-0002-R11 | `stats.test.mjs` diagnostics omit query string, inputs, and other routes |
| SPEC-0002-R12 | `state.test.mjs` host classification |
| SPEC-0002-R13 | `console_test.go` storage scan |
| SPEC-0002-R14 | `state.test.mjs` with a fake document and timers |
| SPEC-0002-R15, R17 | `test/console-smoke/smoke.mjs` accessible names, Tab with visible focus, 360 px on every view |
| SPEC-0002-R16 | `contrast_test.go` computes WCAG ratios of the theme tokens |
| SPEC-0002-R18 | `console_test.go` size, file types, and no external references |
| SPEC-0002-R19 | `stats.test.mjs` server health thresholds and attention filter |
| SPEC-0002-R20 | `test/console-smoke/smoke.mjs` 100 rows, then Show all |

Node's built-in test runner covers the pure modules; a Playwright smoke test
in the Kind job (`E2E_BROWSER=1` in CI) covers R09, R15, R17, and R20.

## Rollout

Ships in one minor release behind no flag: the console is already optional
(`gateway.console.enabled`). Release notes call out the new default view.

## Later phases

Each later phase is a separate spec, written when its dependency is
implemented. None of this is promised by SPEC-0002.

| Phase | Depends on | Scope |
| --- | --- | --- |
| Authenticated console | ADR-0003 client authentication | Sign-in with the customer's authorization server (authorization code with PKCE), token in memory only, signed-in identity, Playground calls with the user's token, `401`/`403` explained. Removes the R12 notice when authentication is on. |
| Policy view | Policy decisions and tool authorization (roadmap item 3) | Read-only view of who may call which tool, and why a call was denied. |
| Audit view | Audit and usage events (roadmap item 4) | Search of audit events for this cluster. |
| Streamable HTTP | Protocol-aware routing (roadmap item 2) | Session and stream inspection in the Playground and Traffic. |

Cross-cluster views, organization settings, and billing belong to the
Kavrynt Cloud UI and are out of scope for the runtime console at every phase.

## Risks

- Splitting `app.js` could regress v1 behavior. Mitigation: the SPEC-0001
  tests and the Kind e2e console checks run unchanged.
- Client-side statistics on 500 records are approximate. The UI labels the
  window (R09) and points to `/metrics` for durable data.
- The no-login notice may alarm evaluators. It states a documented fact, and
  the authenticated phase removes it.
