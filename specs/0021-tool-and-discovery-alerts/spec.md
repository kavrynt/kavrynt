# SPEC-0021: Tool error and discovery alerts

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

Two more Prometheus alerts with runbooks: a tool that keeps failing while its
server is healthy (`KavryntMCPToolErrorsHigh`), and a server whose tools the
Operator cannot discover (`KavryntMCPToolDiscoveryFailing`).

## Problem

`KavryntMCPUpstreamErrorsHigh` (SPEC-0010) fires only for transport and HTTP
failures. A tool that returns `isError` or JSON-RPC errors for most calls is
broken for agents but raises nothing. A server whose tools cannot be listed
stays `Ready` by design, so `KavryntMCPServerNotReady` does not fire either.

## Goals

- Every MCP problem an agent would notice raises a Prometheus alert.

## Non-goals

- Alerting on individual failed calls.
- Console alerts (SPEC-0020).

## Requirements

- **SPEC-0021-R01** The Operator MUST export
  `kavrynt_operator_mcpserver_tools_discovered{namespace,name}` (1 when
  `ToolsDiscovered` is `True`, 0 when `False`, removed with the `MCPServer`).
- **SPEC-0021-R02** The PrometheusRule MUST include `KavryntMCPToolErrorsHigh`:
  per route and tool, the share of `tool_error` and `rpc_error` outcomes over
  10 minutes above `toolErrorRatio` (default 0.2) with at least
  `minToolRequestsPerSecond` traffic, for 10 minutes, severity `warning`.
- **SPEC-0021-R03** The PrometheusRule MUST include
  `KavryntMCPToolDiscoveryFailing`: a `Ready` server whose tools-discovered
  metric is 0 for `discoveryFailingFor` (default 15 minutes), severity
  `warning`.
- **SPEC-0021-R04** Both alerts MUST follow SPEC-0010-R04 (labels, summary,
  description, runbook URL), have runbooks, and have promtool tests that fire
  and stay quiet.

## Acceptance criteria

- Given a tool that answers `isError` for 30% of 100 calls a minute for 10
  minutes, then `KavryntMCPToolErrorsHigh` fires for that route and tool
  (R02).
- Given a `Ready` server whose discovery fails for 15 minutes, then
  `KavryntMCPToolDiscoveryFailing` fires (R03).

## Security and privacy

Labels are route, tool, namespace, and name only; tool names are already
metric labels (bounded per route).

## Compatibility

New metric, alerts, runbooks, and values under `metrics.prometheusRule`.
Additive.

## Documentation

Runbooks, chart README alert table, Operator component doc (metrics).

## Open questions

- Should `rpc_error` count (it includes caller mistakes such as unknown
  methods)?
