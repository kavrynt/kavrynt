# SPEC-0024 plan: Console dashboards

## Design

- **Package.** `internal/gateway/history`, no HTTP or Kubernetes code:
  - `ClassOf(outcome)` maps an outcome to `ok`, `toolError`,
    `upstreamError`, or `other` (R01). `Quantile` moves here from
    `alerts` (`histogram_quantile`: linear interpolation inside the
    bucket), and the alerts window uses it.
  - A series is two rings: 60 one-minute slots and 288 five-minute slots.
    Each slot holds `uint32` counts per outcome class and per latency
    bucket (the 12 bounds of the duration histogram plus `+Inf`); route
    series add `uint64` bytes in and out. A ring remembers its newest slot
    and zeroes the slots it skips when a later one is written, at most the
    ring's length, so no slot carries a timestamp and nothing runs in the
    background (R01, R03).
  - `History.Record` maps the call to its class and bucket, finds the route
    series and, for `tools/call`, the tool series under a read lock, and
    counts under that series' own lock. New series take the write lock.
    Named tool series are capped at `maxTools` across routes; beyond it a
    tool is counted in its route's `other` series (R02). `api.Handler.observe`
    records every call with the same bounded labels as the metrics.
  - Memory with the defaults and 300 routes: a route series is 348 slots ×
    84 bytes (29 KiB), a tool series 348 × 68 bytes (23 KiB); 300 routes,
    500 tools, and 300 `other` tools are about 27 MiB (R02). A test
    measures it.
  - `History.Query(now, range, route, tool)` sums the slots of the range
    (`1h`: one-minute points; `6h`, `24h`: five-minute points), copying one
    series at a time under its lock (R03). Points start at the replica's
    start when that is inside the range (`since`). It returns per-route
    totals for all routes and per-tool totals for all routes and one route.
  - `History.Run` drops the series of routes that are no longer in the
    route table every minute (R02).
- **Alerts (R09).** The evaluator keeps resolved alerts for 24 hours (at
  most 500); `List` still returns only those resolved in the last hour, so
  `/v1/alerts` does not change. `Within(from, to, route, tool)` returns the
  alerts active inside the range about the scope: every alert for all
  routes, the route's alerts for a route, and the route's alerts without a
  tool or for that tool for a tool.
- **Endpoint (R04, R05).** `GET /v1/history` behind `authorized` like
  `/v1/servers`: `{"instance", "range", "stepSeconds", "start", "end",
  "since", "scope", "points", "routes", "tools", "alerts", "grafanaURL"}`.
  Points carry `time`, `ok`, `toolError`, `upstreamError`, `other`,
  `errorRate`, `p50Ms`, `p95Ms`, and, outside a tool scope, `bytesIn` and
  `bytesOut`; values without calls are `null`. `400` with `{"error"}` for
  a `range` other than `1h`, `6h`, `24h`, a `route` the table does not
  route, a `tool` without a `route`, and a tool name `mcpmeta.ValidName`
  rejects (except the label `other`).
- **Values.** `gateway.console.history.maxTools` (default 500, 1 to 10000)
  becomes `--history-max-tools`; `gateway.console.grafanaURL` (empty, or an
  `http`/`https` URL without credentials) becomes `--grafana-url`, which
  the Gateway checks at start and returns in `/v1/history`.
- **Console (R06 to R11).** A new DOM-free `charts.js` (`Kavrynt.charts`):
  `RANGES`, `CLASSES` (labels and outcomes per class; a Go test keeps them
  equal to `ClassOf`), `historyPath`, `chartSeries` (requests per minute
  by class, error rate, p50/p95, bytes per second from the points),
  `chartGeometry` (scales, ticks, stacked bars, line paths, and the hover
  index for a width), `alertMarks`, `topLists`, `dashboardNote`, and
  `grafanaLinks`. `dom.js` gains `svg()`, which creates SVG elements with
  `createElementNS` and text nodes only. `app.js` adds a Dashboard page
  (`#/dashboard?range=&route=&tool=`) and a Dashboard tab on server pages
  (`#/servers/<route>/dashboard?range=&tool=`). It fetches `/v1/history`
  when either opens and every 30 seconds while one is shown, and forgets
  the history on sign-out. Each chart is an `svg` with `tabindex="0"` and
  an `aria-label`; hover and the arrow keys move a cursor whose values
  appear in a live readout; a visually hidden table holds the same
  numbers; alert starts and resolutions are dashed lines with SVG
  `<title>` text. Charts are redrawn at the panel's width when the window
  is resized. Series colours are CSS classes on the existing tokens.
- **Tests.** Go tests in `internal/gateway/history` for recording, the
  rings, the cap, pruning, queries, quantiles, allocation-free recording,
  and memory; API tests for the endpoint, its login, refusals, and
  privacy; alerts tests for the 24-hour retention and `Within`; console
  Go tests for the script order and the classes; contrast pairs for the
  chart colours; `test/console/charts.test.mjs`; chart unit tests for the
  flags.
- **End to end (R12).** `scripts/e2e-kind.sh` checks that the echo tool
  call appears in `/v1/history` for its route and tool, and that `range=7d`
  is refused. The browser smoke test opens the Dashboard page and the
  server's Dashboard tab at 1280 px and 360 px, with charts drawn, no page
  errors, no horizontal scrolling, and named controls.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/history/` | History, rings, queries, classes, quantile |
| `internal/gateway/alerts/alerts.go`, `window.go` | 24-hour retention, `Within`, shared quantile |
| `internal/gateway/api/api.go`, `history.go` | Recording, `/v1/history` |
| `cmd/gateway/main.go` | `--history-max-tools`, `--grafana-url`, pruning |
| `internal/gateway/console/static/` | `charts.js`, `svg()`, Dashboard page and tab, styles |
| `internal/gateway/console/*_test.go` | Script order, classes, chart contrast |
| `test/console/charts.test.mjs` | Console tests |
| `test/console-smoke/smoke.mjs` | Browser checks |
| `charts/kavrynt/charts/gateway/values.yaml`, `values.schema.json`, `templates/deployment.yaml` | Values and flags |
| `charts/kavrynt/values.yaml` | Umbrella defaults, if mirrored |
| `charts/kavrynt/tests/console_history_test.yaml` | Chart tests |
| `scripts/e2e-kind.sh`, `docs/E2E.md` | R12 |
| `docs/components/gateway/README.md`, `charts/kavrynt/README.md`, `README.md`, `CLAUDE.md`, `ROADMAP.md` | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0024-R01 | `internal/gateway/history/history_test.go` |
| SPEC-0024-R02 | `internal/gateway/history/history_test.go` (cap, pruning, `TestMemoryStaysUnderBudget`); `internal/gateway/api/history_test.go` (bounded labels) |
| SPEC-0024-R03 | `internal/gateway/history/history_test.go` `TestRecordIsConstantWork` |
| SPEC-0024-R04 | `internal/gateway/api/history_test.go` |
| SPEC-0024-R05 | `internal/gateway/api/history_test.go` |
| SPEC-0024-R06, R07 | `test/console/charts.test.mjs`; `test/console-smoke/smoke.mjs` |
| SPEC-0024-R08 | `test/console/charts.test.mjs`; `internal/gateway/console/contrast_test.go`; `console_test.go`; `smoke.mjs` |
| SPEC-0024-R09 | `internal/gateway/alerts/alerts_test.go`; `test/console/charts.test.mjs` |
| SPEC-0024-R10 | `test/console/charts.test.mjs`; `charts/kavrynt/tests/console_history_test.yaml`; `cmd/gateway` flag test |
| SPEC-0024-R11 | the tests above |
| SPEC-0024-R12 | `scripts/e2e-kind.sh` "verifying console history"; `test/console-smoke/smoke.mjs` |

## Rollout

Always on with the console; `/v1/history` is behind the console login.
Release notes: the Dashboard page and tab, `/v1/history`, the two values,
the Gateway's added memory, and that each replica charts its own calls.

## Risks

- Each replica charts its own calls, and a restart starts its history
  again. Mitigation: the page says so and points to Grafana (R10).
- More than 300 routes use more than 32 MiB (about 29 KiB per route). The
  Gateway docs give the formula.
- A wall-clock step backwards counts calls in the earlier slot while that
  slot is still in the ring, and drops them when it is not.
