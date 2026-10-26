# SPEC-0023: Custom dashboards

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

Teams can add their own Grafana dashboards through the chart, start from a
second default dashboard focused on one MCP server and its tools, and build
panels from a documented metrics reference. Grafana stays the place for
custom dashboards; the console keeps its fixed operational views.

## Problem

The chart ships one Grafana dashboard ("Kavrynt MCP Traffic"). Teams that
want their own views must copy JSON by hand and manage it outside the
release, and there is no list of the metrics and labels they can use.

## Goals

- A team's dashboards are installed and upgraded with the Kavrynt release.
- Building a custom panel needs no reading of source code.

## Non-goals

- A dashboard builder or saved views inside the console. Grafana does this
  well; the console stays a fixed operational UI (see Decisions).
- Dashboards for other Prometheus-compatible UIs.

## Requirements

- **SPEC-0023-R01** The chart MUST ship a second default dashboard, "Kavrynt
  MCP Server", with a server variable and, for that server: readiness and
  discovery status, request rate, error rate by outcome, p50 and p95 latency
  by tool, top failing tools, and bytes.
- **SPEC-0023-R02** `gateway.metrics.dashboard.extra` MUST accept a map of
  file name (`<name>.json`) to dashboard JSON (a string, for example from
  `--set-file`, or an object), rendered as one additional ConfigMap per
  entry with the same sidecar labels, so custom dashboards are released
  with the chart. A value that is not valid JSON MUST fail the install.
- **SPEC-0023-R03** Every shipped dashboard MUST use only metrics the runtime
  exports, with a `datasource` variable, and a test MUST fail when a
  dashboard query names a metric that does not exist.
- **SPEC-0023-R04** The Gateway and Operator docs MUST contain a metrics
  reference: every metric, its type, its labels and their bounds, and example
  PromQL for common panels (error rate per server, p95 per tool, calls per
  client when audit is not used).
- **SPEC-0023-R05** The chart README MUST show how to export a dashboard
  edited in Grafana and add it through `extra`.

## Acceptance criteria

- Given `gateway.metrics.dashboard.extra: {team.json: <json>}`, then a
  ConfigMap with the sidecar label holds that dashboard (R02).
- Given a dashboard query for `kavrynt_gateway_unknown_metric`, then the
  test fails (R03).

## Security and privacy

Dashboards show metric labels only (route, method, tool, outcome); no
arguments, results, or identities.

## Compatibility

New values and ConfigMaps; existing dashboard unchanged. The server
dashboard reads the Operator's `namespace` and `name` labels, which needs
the Operator's ServiceMonitor to keep them (`honorLabels`, a fix shipped
with this work).

## Documentation

Metrics reference in component docs, chart README (dashboards).

## Decisions

- Grafana only. Saved views in the console (filters and pinned servers per
  user) would need per-user storage in the Gateway; they can be proposed
  separately.
