# SPEC-0018: MCP Streamable HTTP routing

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) (MCP `2026-07-28` baseline) |
| Supersedes | none |

## Summary

The Gateway checks MCP Streamable HTTP requests against the protocol
revision it supports, on every route and not only under policy: the
`Mcp-Method` and `Mcp-Name` headers must match the JSON-RPC body,
batches are refused, protocol versions are validated, and stream resumption
headers pass through.

## Problem

The Gateway forwards any HTTP request on a route. The MCP `2026-07-28`
revision makes `Mcp-Method` and `Mcp-Name` mandatory and removes batches;
the Gateway only cross-checks headers when a policy is on (SPEC-0012-R10),
and does not reject the deprecated HTTP+SSE transport.

## Goals

- Every forwarded request is a well-formed MCP Streamable HTTP request.
- Metrics, policy, and audit see the same method and tool as the server.

## Non-goals

- Translating the deprecated HTTP+SSE transport.
- Proxying `stdio` servers.

## Requirements

- **SPEC-0018-R01** A `POST` whose `Mcp-Method` or `Mcp-Name` header disagrees
  with the JSON-RPC body MUST get `400` and not be forwarded, with or
  without a policy.
- **SPEC-0018-R02** A `POST` without `Mcp-Method` MUST be accepted for
  protocol versions before `2026-07-28` and refused with `400` when
  `MCP-Protocol-Version` is `2026-07-28` or later.
- **SPEC-0018-R03** A JSON-RPC batch (array body) MUST get `400`.
- **SPEC-0018-R04** An `MCP-Protocol-Version` the Gateway does not know MUST
  get `400` with the supported versions.
- **SPEC-0018-R05** `Last-Event-ID` and `Mcp-Session-Id` MUST be forwarded
  unchanged, and `GET` event streams and `DELETE` session requests MUST be
  proxied without a body check.
- **SPEC-0018-R06** Each refusal MUST be recorded with outcome
  `protocol_error` and in the audit event as `invalid`.

## Acceptance criteria

- Given `Mcp-Name: echo` and body `params.name: delete_all`, then `400` with
  no policy configured (R01).
- Given `MCP-Protocol-Version: 2026-07-28` and no `Mcp-Method`, then `400`
  (R02).

## Security and privacy

Stops header smuggling between what intermediaries see and what the server
executes. No new data.

## Compatibility

Clients that send mismatched headers or batches break; the release notes
say so. A value `gateway.config.strictProtocol` (default `true`) MAY allow
turning R01–R04 off during migration.

## Documentation

Gateway component doc (routing), release notes.

## Decisions

- Supported versions are `2024-11-05`, `2025-03-26`, `2025-06-18`,
  `2025-11-25`, and `2026-07-28`, listed once in `internal/mcpmeta` so the
  Gateway and the Operator agree. A request without `MCP-Protocol-Version`
  is treated as `2025-03-26`, as the MCP specification says.
- `Mcp-Name` is checked against `params.name` of `tools/call`, the only
  method whose name the Gateway reads today. A POST whose body has no
  `method` (a JSON-RPC response from the client) needs no `Mcp-Method`.
- `gateway.config.strictProtocol` (default `true`) is included: `false`
  forwards requests that fail R01 to R04, still records them, and keeps the
  SPEC-0012 checks when a policy is on.
