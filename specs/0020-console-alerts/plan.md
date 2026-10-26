# SPEC-0020 plan: Alerts in the console

## Design

- **Package.** `internal/gateway/alerts`, no HTTP or Kubernetes code:
  - `Window` keeps the Gateway's recorded calls for 10 minutes in ten
    one-minute slots: per route the calls, upstream failures
    (`upstream_error`, `upstream_unreachable`, `timeout`), and counts per
    latency bucket (the buckets of
    `kavrynt_gateway_mcp_request_duration_seconds`); per route and tool the
    `tools/call` requests and those ending in `tool_error` or `rpc_error`.
    `api.Handler.observe` records every call it puts in the metrics, with
    the same bounded method and tool labels, so the window holds at most
    the routed servers times 200 tools. p95 is computed like
    `histogram_quantile`: linear interpolation inside the bucket.
  - `Evaluator.Evaluate(now, servers)` checks the five conditions of R01.
    Status conditions come from the route table's `model.Server` list and
    start at their condition's `lastTransitionTime`, so every replica agrees
    (R04). Traffic conditions start when a replica first sees them hold. A
    condition that held for its `for` (R02) is active; one that stops
    holding resolves (`resolvedAt`), or is dropped when it was still
    pending. Alerts about routes that are no longer in the table are
    removed. Resolved alerts are kept for an hour, at most 500.
  - `Evaluator.Run` evaluates every 15 seconds.
  - `Alert`: `name`, `severity` (all `warning`, as their Prometheus alerts),
    `scope` (`route`, optional `tool`), `summary`, `since`, `resolvedAt`,
    `runbookURL` (`<runbookBaseURL>/<Prometheus alert>.md`).
- **Shared values (R03).** The umbrella chart renders ConfigMap
  `<release>-console-alerts` with `config.json` built from
  `metrics.prometheusRule` (`errorRatio`, `minRequestsPerSecond`,
  `latencyP95Seconds`, `toolErrorRatio`, `minToolRequestsPerSecond`,
  `notReadyFor`, `discoveryFailingFor`, and the same `runbookBaseURL` as the
  rules). The Gateway subchart cannot read umbrella values, so its
  Deployment mounts that ConfigMap (`optional: true`) and passes
  `--alerts-config`. The Gateway rereads the file when it changes and keeps
  the last valid one; without it the defaults apply. The fixed `for`
  durations (5, 10, 15 minutes) are constants in both the rules and the
  package.
- **Endpoint (R05).** `GET /v1/alerts` behind `authorized` like
  `/v1/servers`: `{"instance", "evaluatedAt", "alerts": [...]}`, active
  first (critical before warning, oldest first), then resolved (newest
  first). Alerts carry route and tool names and the summary only.
- **Console (R06, R07).** `stats.js` gains the DOM-free `ALERTS`
  definitions, `sortAlerts`, `activeAlerts`, and `alertLink`;
  `app.js` polls `/v1/alerts` with the servers, shows the active count in a
  nav item on every page, adds an Alerts page (active, then resolved, with
  links to the server or tool and the runbook, and the replica note for
  traffic alerts), and links the Overview's "Needs attention" panel to it.
  Signing out forgets the alerts.
- **Tests (R08).** Go tests in `internal/gateway/alerts` for each condition
  firing, pending, and resolving, with a fake clock; API tests for the
  endpoint and its login; `test/console/stats.test.mjs` for the ordering,
  count, and links; a console Go test that keeps `ALERTS` equal to the Go
  condition names.
- **End to end (R09).** `scripts/e2e-kind.sh` installs with
  `metrics.prometheusRule.discoveryFailingFor=30s`, registers an
  unreachable `MCPServer`, waits for `DiscoveryFailing` on `/v1/alerts`,
  deletes the server, and waits for the alert to disappear.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/alerts/` | Window, evaluator, config |
| `internal/gateway/api/api.go`, `traffic.go` | `/v1/alerts`, recording |
| `cmd/gateway/main.go` | `--alerts-config`, evaluator |
| `internal/gateway/console/static/` | Alerts page, nav count, overview link |
| `test/console/stats.test.mjs` | Console tests |
| `charts/kavrynt/templates/console-alerts.yaml` | Shared values ConfigMap |
| `charts/kavrynt/charts/gateway/templates/deployment.yaml` | Mount and flag |
| `charts/kavrynt/tests/console_alerts_test.yaml` | Chart tests |
| `scripts/e2e-kind.sh`, `docs/E2E.md` | R09 |
| `docs/components/gateway/README.md`, `charts/kavrynt/README.md`, `docs/runbooks/README.md`, `CLAUDE.md`, `ROADMAP.md` | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0020-R01, R02 | `internal/gateway/alerts/alerts_test.go` |
| SPEC-0020-R03 | `internal/gateway/alerts/config_test.go`; `charts/kavrynt/tests/console_alerts_test.yaml` |
| SPEC-0020-R04 | `internal/gateway/alerts/alerts_test.go` (status alerts from `lastTransitionTime`); `api` test (instance) |
| SPEC-0020-R05 | `internal/gateway/api/alerts_test.go` |
| SPEC-0020-R06, R07 | `test/console/stats.test.mjs`; `internal/gateway/console/console_test.go` |
| SPEC-0020-R08 | the tests above |
| SPEC-0020-R09 | `scripts/e2e-kind.sh` "verifying console alerts" |

## Rollout

Always on with the console; `/v1/alerts` is behind the console login.
Release notes: the Alerts page, `/v1/alerts`, and that the console alerts
use the `metrics.prometheusRule` thresholds even when the rules are off.

## Risks

- Traffic alerts differ between replicas, and from Prometheus, which sums
  every replica. Mitigation: the console says which replica answered.
- `KavryntMCPLatencyHigh` has no minimum traffic; `SlowRoute` needs
  `minRequestsPerSecond`, so a route with very few slow calls raises only
  the Prometheus alert. The Gateway docs say so.
