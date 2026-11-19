# SPEC-0046: Gateway JSON-RPC error responses on /mcp/ routes

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @vak-rashu |
| Created | 2026-11-16 |
| Issue | [#167](https://github.com/kavrynt/kavrynt/issues/167) |
| ADRs | none |
| Supersedes | none |

## Summary

Every error returned by the Gateway on `/mcp/` routes is formatted as a
standard JSON-RPC 2.0 error object carrying the request's `id` when it could be
read (`null` otherwise), while preserving existing HTTP status codes. MCP
client SDKs and agent frameworks then surface the error message directly to the
agent model instead of failing the HTTP transport with a generic error.
Non-MCP routes (`/v1/*`, `/ui/`, `/healthz`) continue returning standard HTTP
JSON errors.

## Problem

When an error occurs on an `/mcp/` route today, some answers return JSON-RPC
error objects (such as policy denials with code `-32001` or oversized bodies with
code `-32600`), while others return a plain `{"error":"..."}` object via
`writeError` in `internal/gateway/api/api.go`.

MCP client SDKs (such as the official Model Context Protocol Python and
TypeScript SDKs) treat a non-JSON-RPC response on a Streamable HTTP route as an
unparseable transport failure. The failure message is swallowed or surfaces as a
raw HTTP error, preventing the AI agent or user from understanding why the call
failed (for example, whether the route was unregistered, the call timed out, or
the origin was forbidden).

## Goals

- Every error returned on `/mcp/` routes uses the JSON-RPC 2.0 error response
  shape (`jsonrpc`, `id`, and `error` with `code` and `message`).
- The JSON-RPC error carries the request's `id` if it was read, or `null` if the
  body could not be read or was not a valid JSON-RPC request.
- Error codes align with the JSON-RPC 2.0 specification: standard protocol
  error codes (`-32600`, `-32601`, `-32602`, `-32603`) and the reserved
  implementation-defined server error range (`-32000` to `-32099`).
- Existing HTTP status codes (`400`, `403`, `404`, `405`, `413`, `502`, `503`,
  `504`) are preserved for HTTP clients and metrics.

## Non-goals

- Changing errors on administrative, console, or metrics endpoints (`/v1/*`,
  `/ui/`, `/healthz`, `/readyz`): these are standard web/REST APIs, not MCP
  transport routes.
- Changing existing policy denial formatting (SPEC-0036, code `-32001` with HTTP
  `200` on denied POST requests).
- Changing tool results (`isError: true`): protocol errors represent Gateway and
  transport failures, not the tool execution output.

## Requirements

- **SPEC-0046-R01** Every error response sent by the Gateway for a request to an
  `/mcp/` route MUST have `Content-Type: application/json` and a JSON-RPC 2.0
  error body containing `"jsonrpc": "2.0"`, `"id"`, and an `"error"` object
  with integer `"code"` and string `"message"`.
- **SPEC-0046-R02** The `"id"` field of an `/mcp/` error response MUST match the
  request's JSON-RPC `id` if the body was successfully parsed as a JSON-RPC
  request; otherwise, `"id"` MUST be `null`.
- **SPEC-0046-R03** Invalid requests (disallowed browser `Origin`, disallowed
  HTTP methods, request path containing `.` or `..` segments, or oversized
  bodies) MUST return JSON-RPC error code `-32600` (`Invalid Request`).
- **SPEC-0046-R04** Requests with invalid or malformed parameter structures
  (such as `tools/call` with a non-string or duplicate `params.name`) MUST
  return JSON-RPC error code `-32602` (`Invalid params`).
- **SPEC-0046-R05** Requests targeting an unregistered server route or an
  unforwarded subpath MUST return JSON-RPC error code `-32601` 
  (`Method not found`) with HTTP status `404`.
- **SPEC-0046-R06** Gateway and upstream infrastructure failures MUST return
  error codes in the JSON-RPC reserved server error range (`-32000` to
  `-32099`):
  - Session not found: code `-32002` (HTTP `404`)
  - Upstream timeout: code `-32003` (HTTP `504`)
  - Upstream credential unavailable: code `-32004` (HTTP `503`)
  - Audit queue full: code `-32005` (HTTP `503`)
  - Upstream request failure or unsupported redirect: code `-32000` (HTTP `502`)
- **SPEC-0046-R07** Existing HTTP status codes for all `/mcp/` error responses
  MUST be preserved.
- **SPEC-0046-R08** Error responses on non-MCP routes (`/v1/*`, `/ui/`,
  `/healthz`, `/readyz`, `/metrics`) MUST NOT return JSON-RPC error objects.

## Acceptance criteria

- Given an `/mcp/` request to an unregistered route (e.g. `/mcp/ns.nope`),
  when sent by a client, then the response has HTTP status `404` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32601,"message":"MCP server \"ns.nope\" is not registered"}}` (SPEC-0046-R01, SPEC-0046-R02, SPEC-0046-R05, SPEC-0046-R07).
- Given a valid JSON-RPC `POST` to an `/mcp/` route with `id: 42` that times out
  waiting for upstream, when the request timeout expires, then the response has
  HTTP status `504` and body `{"jsonrpc":"2.0","id":42,"error":{"code":-32003,"message":"upstream request timed out"}}` (SPEC-0046-R01, SPEC-0046-R02, SPEC-0046-R06, SPEC-0046-R07).
- Given an `/mcp/` request with a disallowed browser `Origin`, when evaluated by
  the Gateway, then the response has HTTP status `403` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"origin not allowed; add it with --allowed-origins"}}` (SPEC-0046-R01, SPEC-0046-R03, SPEC-0046-R07).
- Given a request to `/v1/servers` without credentials, when rejected, then the
  response is a standard HTTP `401` with plain JSON `{"error":"login required"}` and
  does not contain `"jsonrpc"` (SPEC-0046-R08).

## Security and privacy

Error messages returned in the JSON-RPC error object must not expose internal
network topologies, backend pod IP addresses, or secrets. Error messages remain
bounded to existing sanitized messages already returned by the Gateway.

## Compatibility

This change affects MCP clients communicating over Streamable HTTP on `/mcp/`
routes. Clients that parse JSON-RPC error bodies will now receive actionable
error structures instead of unhandled transport errors. HTTP status codes and
audit event records remain unchanged. No CRD changes or Helm value changes are
required.

## Documentation

Update Gateway component documentation to document JSON-RPC error codes returned
on `/mcp/` routes.

## Open questions

- None.
