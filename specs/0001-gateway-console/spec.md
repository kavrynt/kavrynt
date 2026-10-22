# SPEC-0001: Gateway web console

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-22 |
| Issue | none (written after the fact) |
| ADRs | [ADR-0001](../../docs/ADR-0001-Runtime-Monorepo.md), [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

This spec records the console as it was shipped, so later console work
([SPEC-0002](../0002-console-v2/spec.md)) has a tested baseline to build on.

## Summary

The Gateway serves a read-only web console at `/ui/` that shows registered
MCP servers and their tools, lets an operator call tools through the
Gateway's own routes, and shows recent traffic. It is embedded in the Gateway
binary and reads only the Gateway's JSON endpoints.

## Problem

Operators could only inspect the runtime with `kubectl` and `curl`. They had
no quick way to see why a server was not routed, what tools it exposes, or
whether calls through the Gateway succeed.

## Goals

- Show every `MCPServer` the Gateway knows, its routing state, and its tools.
- Call tools through the Gateway exactly as an agent would.
- Show recent request metadata with per-route error rate and latency.

## Non-goals

- Authentication and authorization of console users. The console has the
  Gateway's security boundary until [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md)
  is implemented.
- Creating, changing, or deleting `MCPServer` resources. The Gateway is
  read-only on the Kubernetes API.
- Durable or cross-replica traffic history. `/metrics` is the durable signal.
- Cross-cluster inventory, which belongs to Kavrynt Cloud.

## Requirements

- **SPEC-0001-R01** When the console is enabled, the Gateway MUST serve it at
  `/ui/` and MUST redirect `GET /` and `GET /ui` to `/ui/`.
- **SPEC-0001-R02** When the console is disabled (`--console=false`), the
  Gateway MUST answer `/ui/` with `404`.
- **SPEC-0001-R03** Console paths MUST accept only `GET` and `HEAD` and MUST
  answer other methods with `405` and `Allow: GET, HEAD`.
- **SPEC-0001-R04** Every console response MUST carry a
  `Content-Security-Policy` that allows scripts, styles, and connections only
  from the Gateway's own origin and forbids framing, plus
  `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
- **SPEC-0001-R05** The console MUST insert untrusted data (server, tool, and
  traffic metadata, and MCP responses) as text only. Its scripts MUST NOT use
  `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `eval`, or
  `new Function`.
- **SPEC-0001-R06** `GET /v1/servers` MUST list every `MCPServer` the Gateway
  watches with its route, whether it is routed, its conditions, and its
  discovered tools with their input schemas.
- **SPEC-0001-R07** `GET /v1/traffic` MUST return metadata of the most recent
  proxied requests, newest first and bounded to 500 per replica: route,
  method, tool, outcome, status, duration, and byte counts.
- **SPEC-0001-R08** `GET /v1/traffic` MUST NOT contain request or response
  bodies, tool arguments, or tool results.

## Acceptance criteria

- Given a Gateway with the console enabled, when a browser opens `/`, then it
  lands on `/ui/` and the page loads with only same-origin requests (R01, R04).
- Given an `MCPServer` with a discovered `echo` tool, when the console lists
  servers, then the server is shown as routed with the `echo` tool (R06).
- Given a `tools/call` of `echo` with argument `secret-argument`, when
  `/v1/traffic` is read, then the call is listed with outcome `ok` and the
  argument appears nowhere in the response (R07, R08).

## Security and privacy

The console holds no credentials, sets no cookies, and writes no Kubernetes
resources. Anyone who can reach the Gateway can use it, exactly as they can
call `/mcp/` routes; this is documented in `SECURITY.md` and the Gateway
component doc. MCP metadata is untrusted and only rendered as text (R05)
under a same-origin CSP (R04).

## Compatibility

No CRD change. Helm value `gateway.console.enabled` (default `true`) maps to
`--console`.

## Documentation

[Gateway component doc](../../docs/components/gateway/README.md#console),
README "Open the console", and `SECURITY.md` known limitations.

## Open questions

None.
