# SPEC-0024: Console dashboards

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-25 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

The console gains a Dashboard page with charts of MCP traffic over the last
hour, 6 hours, or 24 hours: requests by outcome, error rate, latency, and
bytes, for all servers or one server or tool, with the busiest and most
failing servers and tools. The Gateway keeps the history itself, in memory
and bounded, so the dashboard works without Prometheus or Grafana. Grafana
stays the place for custom and cluster-wide dashboards (SPEC-0023).

## Problem

The console shows only the last 500 requests per Gateway replica
(`/v1/traffic`), which at moderate traffic is a few seconds or minutes.
Nobody can see in the console whether errors started an hour ago, whether
latency is rising, or which server is busiest today. That needs Prometheus
and Grafana (SPEC-0023), which trial users and small teams often do not run.

## Goals

- See MCP traffic trends for up to 24 hours in the console, with no extra
  component.
- Go from "something is wrong" to the server, tool, and time it started in
  a few clicks, together with the console alerts (SPEC-0020).

## Non-goals

- A dashboard builder, saved layouts, or arbitrary queries in the console
  (SPEC-0023 keeps that in Grafana).
- History beyond 24 hours, or history that survives a Gateway restart.
- Cluster-wide numbers summed over replicas (see Open questions).
- Per-caller charts: metrics carry no identity; audit events (SPEC-0013)
  are the per-caller record.

## Requirements

### History

- **SPEC-0024-R01** The Gateway MUST keep, in memory, per route: calls,
  calls by outcome class (`ok`, tool errors: `tool_error` and `rpc_error`,
  upstream failures: `upstream_error`, `upstream_unreachable`, `timeout`,
  refused by the Gateway: every other outcome), latency in the buckets of
  `kavrynt_gateway_mcp_request_duration_seconds`, and bytes in and out; per
  route and tool: `tools/call` requests, tool errors, and latency buckets.
  Points MUST be one minute apart for the last hour and five minutes apart
  for the last 24 hours.
- **SPEC-0024-R02** History MUST use the same bounded labels as the metrics
  (SPEC-0010-R06), MUST be capped at `gateway.console.history.maxTools`
  tools (default 500) across all routes, with further tools counted as
  `other`, and MUST stay under 32 MiB per replica with the defaults. Routes
  of deleted `MCPServer`s are dropped.
- **SPEC-0024-R03** Recording a call MUST NOT add more than constant work to
  the request path and MUST NOT block it.

### API

- **SPEC-0024-R04** `GET /v1/history?range=1h|6h|24h[&route=<route>[&tool=<tool>]]`
  MUST return the points of the range for all routes together, one route, or
  one tool: time, calls by outcome class, error rate, p50 and p95 latency
  (computed like `histogram_quantile`), and bytes; plus, for the range, the
  ten routes and tools with the most calls and with the highest error rate
  (at least 20 calls). It MUST name the replica (`instance`) and when its
  history starts (`since`, the replica's start when that is inside the
  range). It MUST be behind the console login like `/v1/servers` and MUST
  NOT contain tool arguments, results, identities, or credentials.
- **SPEC-0024-R05** Unknown `range` values, routes that are not routed, and
  malformed tool names MUST be refused with `400` and a stable JSON error.

### Console

- **SPEC-0024-R06** The console MUST have a Dashboard page with a range
  selector (1h, 6h, 24h) and charts of requests per minute by outcome class,
  error rate, p50 and p95 latency, and bytes per second, and the top lists
  of R04 linking to their server or tool. The selected range and scope MUST
  be part of the URL.
- **SPEC-0024-R07** Each server's detail page MUST have a Dashboard tab with
  the same charts for that server and a per-tool table (calls, errors, error
  rate, p95) for the range; choosing a tool MUST show its charts.
- **SPEC-0024-R08** Charts MUST be inline SVG drawn by the console's own
  code: no external scripts, styles, or fonts, nothing inserted as HTML, and
  only the console's colour tokens, so both themes and the existing
  contrast test apply. Each chart MUST show its values on hover and keyboard
  focus and MUST have a table of the same numbers for screen readers.
- **SPEC-0024-R09** Charts MUST mark the start and resolution of console
  alerts (SPEC-0020) about the shown scope inside the range.
- **SPEC-0024-R10** The page MUST say that the numbers are this replica's
  since its start, and link the Grafana dashboards (SPEC-0023) for
  cluster-wide history.
- **SPEC-0024-R11** Chart geometry, outcome classes, and the top lists MUST
  be computed in DOM-free functions that `make console-test` covers, and
  history in Go code that `make test` covers.

### End to end

- **SPEC-0024-R12** In the e2e, tool calls through a route MUST appear in
  `/v1/history` for that route and tool within a minute, and the browser
  smoke test MUST render the Dashboard page and a server's Dashboard tab at
  360 px and 1280 px without page errors or horizontal scrolling.

## Acceptance criteria

- Given a server whose tool started failing 40 minutes ago, when the
  Dashboard page shows the last hour for that server, then the error-rate
  chart rises 40 minutes ago, the alert's start is marked, and the tool
  heads the "highest error rate" list (R01, R04, R06, R09).
- Given 24 hours of traffic to 300 servers with 200 tools each, then the
  Gateway's history stays under 32 MiB and further tools are counted as
  `other` (R02).
- Given a Gateway replica restarted 2 hours ago, when the 24-hour range is
  selected, then the charts start 2 hours ago and the page says so (R04,
  R10).

## Security and privacy

`/v1/history` is behind the console login and carries route and tool names,
counts, latencies, and sizes only, the same data as `/metrics`. Route and
tool names come from `MCPServer`s and MCP servers and are untrusted: the
console inserts them as text and SVG text nodes only. No new outbound
access, Kubernetes permissions, or stored data.

## Compatibility

New endpoint, console page and tab, and values under
`gateway.console.history` (`maxTools`). The Gateway uses more memory (R02):
the chart's default memory limit (256 Mi) is reviewed in the plan. No CRD
change.

## Documentation

Gateway component doc (console, endpoint, memory), chart README (console
and values), `ROADMAP.md`, the console section of the README.

## Open questions

- Should the dashboard also read Prometheus when one is configured
  (`gateway.console.prometheusURL`), for cluster-wide history beyond 24
  hours? That adds outbound access from the Gateway to Prometheus and a
  fixed set of queries; this spec assumes in-memory history only.
- Is 24 hours the right maximum, or should `gateway.console.history`
  allow up to 7 days at coarser resolution (more memory)?
- Should the Overview page gain small trend lines (sparklines) from the
  same history, or stay as it is?
