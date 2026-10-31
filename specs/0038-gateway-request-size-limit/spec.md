# SPEC-0038: Request size limit for MCP routes

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-31 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

The Gateway refuses an MCP request whose body is larger than a configured
limit, 4 MiB by default, with `413`, and never sends an MCP server more
than the limit.

## Problem

The Gateway reads at most 1 MiB of a request body to classify it, then
streams the rest to the MCP server without a bound. In a trial of
`1.0.0-beta.4`, one 60 MB `tools/call` through the Gateway made the sample
MCP server run out of memory: Kubernetes killed it (`OOMKilled`) and the
caller got `502`. Anyone who can call a route can take down the server
behind it, and MCP servers are often small processes with tight memory
limits that trust the Gateway in front of them.

## Goals

- Bound what one request can send an MCP server.
- Refuse an oversized request before any of it reaches the server when its
  size is known, and with a clear status in every case.

## Non-goals

- Limiting response sizes from MCP servers.
- Per-route or per-client limits.
- Limiting the request rate.

## Requirements

- **SPEC-0038-R01** The Gateway MUST answer `413` to a request to an
  `/mcp/` route whose `Content-Length` is larger than the request size
  limit, without connecting to the MCP server. A `POST` gets a JSON-RPC
  error response with `id` `null` and code `-32600`; other methods get the
  Gateway's JSON error.
- **SPEC-0038-R02** For a request body without `Content-Length`, the
  Gateway MUST forward at most the request size limit to the MCP server,
  and MUST answer `413` when the body is larger, unless an earlier check
  refuses it first: with the tool authorization policy on, a body too
  large to classify (over 1 MiB) is refused with `400` by SPEC-0012
  before anything is forwarded.
- **SPEC-0038-R03** The request size limit MUST default to 4 MiB
  (4194304 bytes) and MUST be set with the Gateway flag
  `--max-request-bytes` and the chart value
  `gateway.config.maxRequestBytes`; the chart MUST reject a value that is
  not an integer between 1024 and 1073741824.
- **SPEC-0038-R04** A refused oversized request MUST be counted with
  outcome `client_error` and status `413`, and its audit event MUST have
  decision `invalid` and a reason that names the limit.

## Acceptance criteria

- Given the default chart, when a client sends a 60 MB `tools/call`, then
  it gets `413` and the MCP server receives nothing (R01, R03).
- Given a chunked request body of 5 MiB and no tool authorization policy,
  then the MCP server receives at most 4 MiB and the client gets `413`;
  with the policy on, the client gets `400` and the server receives
  nothing (R02).
- Given `gateway.config.maxRequestBytes: 10485760`, then a 6 MiB call is
  forwarded (R03).
- Given `gateway.config.maxRequestBytes: 0`, then the install fails (R03).
- Given any refused oversized request, then the metrics, the console
  traffic view, and the audit event show it as a client error (R04).

## Security and privacy

The limit protects MCP servers from memory exhaustion through the Gateway.
It does not protect a server reached directly; the chart's NetworkPolicies
close that path. The refusal carries no request data.

## Rollout and compatibility

New behavior in the next release, on by default. A client that sends MCP
requests larger than 4 MiB gets `413` until the limit is raised. Release
notes: "The Gateway refuses MCP requests larger than
`gateway.config.maxRequestBytes` (4 MiB by default) with `413`
(SPEC-0038)."
