# SPEC-0010: Alerts and runbooks

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

The chart can install Prometheus alerting rules for the Gateway, the
Operator, and registered MCP servers, each linked to a runbook in this
repository that says what the alert means, how to confirm it, and how to
fix it. The Operator exposes its metrics through a Service and reports the
readiness of every `MCPServer` as a metric, so alerts cover the whole
control plane.

## Problem

The Gateway exports MCP traffic metrics and a Grafana dashboard, but
nothing tells an on-call engineer when routing stops, an MCP server goes
down, or the Kavrynt Cloud connector stops syncing. The Operator's metrics
endpoint has no Service, so the Prometheus Operator cannot scrape it, and no
metric says which `MCPServer` is not `Ready`. There are no runbooks.

## Goals

- Every failure mode an operator must act on raises one alert.
- Each alert links to a runbook with diagnosis and fix steps.
- Alert rules are tested offline in CI.

## Non-goals

- Alertmanager routing, receivers, or paging integration.
- SLO dashboards or burn-rate alerts (later, once traffic data exists from
  real installs).
- Alerts for authentication, policy, and audit outcomes; each of those specs
  adds its alerts when it ships (SPEC-0006, SPEC-0012, SPEC-0013).

## Requirements

### Metrics

- **SPEC-0010-R01** The Operator MUST export
  `kavrynt_operator_mcpserver_ready{namespace,name}` with value 1 when the
  server's `Ready` condition is `True` and 0 otherwise, and MUST remove the
  series when the `MCPServer` is deleted.
- **SPEC-0010-R02** With `operator.metrics.service.enabled=true` (default),
  the chart MUST render a ClusterIP Service for the Operator's metrics
  port, and with `operator.metrics.serviceMonitor.enabled=true` a
  ServiceMonitor for it.

### Rules

- **SPEC-0010-R03** With `metrics.prometheusRule.enabled=true` (off by
  default), the chart MUST render one PrometheusRule containing at least:
  `KavryntGatewayDown`, `KavryntGatewayRouteSyncFailing`,
  `KavryntMCPUpstreamErrorsHigh`, `KavryntMCPLatencyHigh`,
  `KavryntOperatorDown`, `KavryntMCPServerNotReady`, and
  `KavryntCloudSyncStale` (rendered only when the Cloud connector is on).
- **SPEC-0010-R04** Every alert MUST carry `severity` (`critical` or
  `warning`) and `component` labels, and `summary`, `description`, and
  `runbook_url` annotations; `runbook_url` MUST point to the runbook for
  that alert at the chart's release tag.
- **SPEC-0010-R05** Thresholds and durations for the error-ratio, latency,
  and not-ready alerts MUST be configurable through values, and extra
  labels MUST be addable to every rule (for routing in Alertmanager).
- **SPEC-0010-R06** Alert expressions MUST use only labels with bounded
  values (route, method, outcome, namespace, name), never tool arguments.

### Runbooks

- **SPEC-0010-R07** `docs/runbooks/` MUST contain one runbook per alert,
  named after the alert, with sections Meaning, Impact, Diagnosis, and
  Mitigation, and CI MUST fail when an alert has no runbook or a runbook
  has no alert.

### Tests

- **SPEC-0010-R08** Every alert MUST have a `promtool test rules` case that
  fires it and one that does not, and CI MUST run them and
  `promtool check rules` on the rendered PrometheusRule.

## Acceptance criteria

- Given an `MCPServer` that is not `Ready` for longer than the threshold,
  then `KavryntMCPServerNotReady` fires with its namespace and name, and
  its runbook explains how to read the `Ready` condition (R01, R03, R07).
- Given 20% of calls to a route end `upstream_error`, `upstream_unreachable`,
  or `timeout` for 10 minutes, then `KavryntMCPUpstreamErrorsHigh` fires for
  that route; at 1% it does not (R03, R05, R08).
- Given `metrics.prometheusRule.enabled=true` and `cloud.enabled=false`,
  then `KavryntCloudSyncStale` is not rendered (R03).
- Given a new alert without a runbook file, then CI fails (R07).

## Security and privacy

Metrics and alerts carry only route, method, outcome, namespace, and name
labels. The new Operator Service exposes `/metrics` inside the cluster,
like the Gateway's; it contains no secrets or tool data. The
`kavrynt_operator_mcpserver_ready` series reveals server names to whoever
can read Prometheus, which already sees Gateway route labels.

## Compatibility

New values: `metrics.prometheusRule` (umbrella chart, holds all rules),
`operator.metrics.service`, `operator.metrics.serviceMonitor`. The Operator
Service is new but on by default; it changes no existing object. The
PrometheusRule needs the Prometheus Operator CRDs, so it is off by default.
No CRD change.

## Documentation

`docs/runbooks/`, chart README (monitoring section), Gateway and Operator
component docs (metrics), README observability line.

## Decisions

- All rules in one PrometheusRule rendered by the umbrella chart rather than
  one per subchart: one switch, one place for shared labels, and the
  umbrella sees both components' values (the Cloud alert depends on
  `operator.cloud.enabled`).
- `promtool` runs from the official Prometheus container image pinned by
  digest, not a new Go dependency.
