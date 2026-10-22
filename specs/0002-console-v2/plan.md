# SPEC-0002 plan: Console v2: operations UI

Proposed design for review alongside the Draft spec. It becomes binding when
the spec is `Accepted`.

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

- **Modules.** Split `app.js` (about 1,100 lines) into ES modules loaded with
  `<script type="module">`, which the current CSP (`script-src 'self'`)
  already allows: `dom.js` (`h`, `put`, `clear`), `state.js` (polling,
  visibility, URL state), `stats.js` (pure: percentiles, per-tool summary,
  attention list, outcome definitions), and one module per view. No bundler.
- **Pure logic first.** Everything R01–R11 compute lives in `stats.js` and
  `state.js` as functions of `/v1/servers` and `/v1/traffic` JSON, so it can
  be tested without a browser.
- **Outcome definitions** (R08) are one table in `stats.js`, copied from the
  Gateway component doc. A Go test checks that every outcome constant in
  `internal/gateway/api/traffic.go` has a definition, so the two cannot drift.
- **Polling** (R14) listens to `visibilitychange`; hidden pages clear their
  timers.
- **Storage** (R13): only the theme preference is stored. A Go test scans the
  static files and fails on any `localStorage`, `sessionStorage`,
  `indexedDB`, or `document.cookie` use outside the theme helper.
- **Diagnostics** (R11) are built from already-fetched JSON; the endpoint is
  shown without its query string.
- **Size budget** (R18): a Go test sums the embedded files.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/console/static/index.html` | Overview and Tools nav, module script, notice slot |
| `internal/gateway/console/static/*.js` | Split into modules; new Overview and Tools views |
| `internal/gateway/console/static/app.css` | Notice banner, narrow layout, focus styles |
| `internal/gateway/console/console_test.go` | Storage, size, and module checks |
| `internal/gateway/api/traffic_test.go` | Outcome definitions match outcome constants |
| `internal/gateway/console/static/*.test.mjs` | Node tests for pure modules (if approved) |
| `scripts/e2e-kind.sh` | Headless smoke test of each view (if approved) |
| `docs/components/gateway/README.md` | Console section |

The Gateway's Go code and its endpoints do not change.

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0002-R01, R02, R03 | `stats.test.mjs` overview and attention cases; e2e smoke opens `#/overview` |
| SPEC-0002-R04, R05 | `stats.test.mjs` catalog search; e2e smoke |
| SPEC-0002-R06, R07 | `stats.test.mjs` filters and per-tool summary |
| SPEC-0002-R08 | `traffic_test.go` every outcome has a definition |
| SPEC-0002-R09 | e2e smoke checks the scope note on traffic views |
| SPEC-0002-R10 | `state.test.mjs` hash round trip |
| SPEC-0002-R11 | `stats.test.mjs` diagnostics omit query string and inputs |
| SPEC-0002-R12 | `state.test.mjs` host classification; e2e smoke through a non-loopback host |
| SPEC-0002-R13 | `console_test.go` storage scan; e2e smoke inspects storage after a call |
| SPEC-0002-R14 | `state.test.mjs` with a fake document visibility |
| SPEC-0002-R15, R16, R17 | e2e smoke with keyboard-only navigation, contrast check of theme tokens, 360 px viewport |
| SPEC-0002-R18 | `console_test.go` size budget |

The test tooling choice is the spec's open question. The alternative to Node
and a headless browser is Go-only checks plus a manual release checklist for
R15–R17, which would leave those requirements untraceable and therefore keep
the spec out of `Implemented`.

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
