# SPEC-0020: Alerts in the console

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

The Gateway evaluates a small set of MCP health conditions itself and the
console shows the ones that are active on a new Alerts page and as a count in
the navigation, with when each started and what to do. Teams without
Prometheus and Alertmanager still see problems; teams with them get the same
conditions as Prometheus alerts (SPEC-0010, SPEC-0021).

## Problem

Problems are visible only in Prometheus alerts, which need the Prometheus
Operator, or in the Overview's "needs attention" list, which the browser
computes from the last 500 requests and forgets on reload. Nobody can see in
the console what is wrong, since when, and how to fix it.

## Goals

- Every active MCP problem is visible in the console with its start time,
  scope (server or tool), and a link to its runbook.
- No Prometheus is needed for the console alerts.
- The conditions match the Prometheus alerts, so the two never disagree on
  what is a problem.

## Non-goals

- Notifications (SPEC-0022 covers Alertmanager).
- Acknowledging, silencing, or assigning alerts.
- Alert history beyond what is active now and what resolved in the last hour.

## Requirements

### Conditions

- **SPEC-0020-R01** The Gateway MUST evaluate, at least every 30 seconds:
  `ServerNotReady` (an `MCPServer`'s `Ready` is not `True`), `DiscoveryFailing`
  (`ToolsDiscovered` is `False` with a failure reason), `UpstreamErrors` (over
  `errorRatio`, default 5%, of a route's calls in the last 10 minutes ended in
  `upstream_error`, `upstream_unreachable`, or `timeout`, with at least 20
  calls), `ToolErrors` (over `toolErrorRatio`, default 20%, of a tool's calls in
  the last 10 minutes ended in `tool_error` or `rpc_error`, with at least 10
  calls), and `SlowRoute` (p95 latency of a route over `latencyP95Seconds`,
  default 5, in the last 10 minutes, with at least 20 calls).
- **SPEC-0020-R02** A condition MUST become active only after it has held for
  its `for` duration (default 5 minutes; `ServerNotReady` 10 minutes), and MUST
  resolve when it no longer holds.
- **SPEC-0020-R03** Thresholds and durations MUST be Helm values shared with
  the Prometheus rules (`metrics.prometheusRule.*`), so both use the same
  numbers.
- **SPEC-0020-R04** Traffic-based conditions MUST use the Gateway's own
  recorded calls; each replica evaluates its own traffic and the console says
  so. Status-based conditions (`ServerNotReady`, `DiscoveryFailing`) MUST be
  the same on every replica.

### API and console

- **SPEC-0020-R05** `GET /v1/alerts` MUST list active alerts and those
  resolved in the last hour: name, severity, scope (`route`, optional
  `tool`), summary, `since`, `resolvedAt`, and `runbookURL`, behind the
  console login like `/v1/servers`. It MUST NOT contain tool arguments,
  results, or credentials.
- **SPEC-0020-R06** The console MUST show an Alerts page listing active
  alerts first (critical before warning, then oldest first), then recently
  resolved ones, each with a link to the server or tool and to its runbook.
- **SPEC-0020-R07** The navigation MUST show the number of active alerts on
  every page, and the Overview MUST link its "needs attention" list to the
  Alerts page.
- **SPEC-0020-R08** Alerts MUST be computed in pure functions (DOM-free) or Go
  code that `make test` and `make console-test` cover, with each condition
  tested firing, pending, and resolving.

### End to end

- **SPEC-0020-R09** In the e2e, an `MCPServer` that cannot be reached MUST
  appear on `/v1/alerts` as `DiscoveryFailing` once its `for` duration (set
  short in the e2e) has passed, and disappear after the server is deleted.

## Acceptance criteria

- Given a server whose tool fails 30% of 50 calls in 10 minutes, when 5
  minutes pass, then the Alerts page shows `ToolErrors` for that tool with the
  time it started and a runbook link (R01, R02, R06).
- Given the failures stop, then the alert moves to "resolved" (R02, R05).
- Given 3 active alerts, then the navigation shows 3 on every page (R07).

## Security and privacy

`/v1/alerts` is behind the console login. It carries route and tool names
and counts only. No new outbound access.

## Compatibility

New endpoint, console page, and values (shared with SPEC-0010). Additive.

## Documentation

Gateway component doc (alerts, endpoint), console section, runbooks for the
new conditions, chart README.

## Open questions

- Should alerts be evaluated cluster-wide (one Gateway replica as leader, or
  the Operator) instead of per replica for traffic conditions?
- Should resolved alerts be kept longer than an hour?
