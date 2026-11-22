# SPEC-0050: Gateway JSON-RPC error responses on /mcp/ routes

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @vak-rashu |
| Created | 2026-11-16 |
| Issue | [#167](https://github.com/kavrynt/kavrynt/issues/167) |
| ADRs | none |
| Supersedes | SPEC-0044 (in part: R05) |

## Summary

Every error returned by the Gateway on `/mcp/` routes—across all supported HTTP
methods (`POST`, `GET` event streams, and `DELETE` session requests)—is formatted
as a standard JSON-RPC 2.0 error object carrying the request's `id` when it could
be read (`null` otherwise, including all `GET` and `DELETE` requests), while
preserving existing HTTP status codes. MCP client SDKs and agent frameworks then
surface the error message directly to the agent model instead of failing the HTTP
transport with a generic error.
Invalid `tools/call` parameter structures (such as `params.name`) return
standard JSON-RPC 2.0 error code `-32602` (`Invalid params`), refining
SPEC-0044-R05. Non-MCP routes (`/v1/*`, `/ui/`, `/healthz`) continue returning
standard HTTP JSON errors.

## Problem

When an error occurs on an `/mcp/` route today, some answers return JSON-RPC
error objects (such as policy denials with code `-32001` or oversized bodies with
code `-32600`), while others return a plain `{"error":"..."}` object via
`writeError` in `internal/gateway/api/api.go`. In particular, `writeRPCError` is
currently used only for `POST` requests, while failed `GET` event stream handshakes
and failed `DELETE` session requests receive plain `writeError` JSON objects.

MCP client SDKs (such as the official Model Context Protocol Python and
TypeScript SDKs) treat a non-JSON-RPC response on a Streamable HTTP route as an
unparseable transport failure. The failure message is swallowed or surfaces as a
raw HTTP error, preventing the AI agent or user from understanding why the call
failed (for example, whether the route was unregistered, the call timed out, or
the origin was forbidden).

## Goals

- Every error returned on `/mcp/` routes across all supported HTTP methods
  (`POST`, `GET`, `DELETE`) uses the JSON-RPC 2.0 error response shape (`jsonrpc`,
  `id`, and `error` with `code` and `message`).
- The JSON-RPC error carries the request's `id` if it was read, or `null` if the
  body could not be read, was not a valid JSON-RPC request, was evaluated before
  reading the body (such as shape, origin, or authentication checks), or when the
  request carries no single request `id` (notifications without `id`, batches,
  `GET` event streams, `DELETE` sessions).
- Error codes align with the JSON-RPC 2.0 specification: standard protocol
  error codes (`-32600`, `-32602`, `-32700`) and the reserved
  implementation-defined server error range (`-32000` to `-32099`).
- Existing HTTP status codes (`400`, `401`, `403`, `404`, `405`, `408`, `413`,
  `415`, `502`, `503`, `504`) are preserved for HTTP clients and metrics.

## Non-goals

- Changing errors on administrative, console, or metrics endpoints (`/v1/*`,
  `/ui/`, `/healthz`, `/readyz`): these are standard web/REST APIs, not MCP
  transport routes.
- Changing existing policy denial formatting (SPEC-0036, code `-32001` with HTTP
  `200` on denied POST requests).
- Changing oversized body handling (SPEC-0038, code `-32600` with HTTP `413`).
- Changing invalid JSON-RPC method checks (SPEC-0044, code `-32600` with HTTP `400`).
- Changing tool results (`isError: true`): protocol errors represent Gateway and
  transport failures, not the tool execution output.

## Requirements

- **SPEC-0050-R01** Every error response sent by the Gateway for a request to an
  `/mcp/` route (including `POST` requests, `GET` event streams, and `DELETE`
  session requests) MUST have `Content-Type: application/json` and a JSON-RPC 2.0
  error body containing `"jsonrpc": "2.0"`, `"id"`, and an `"error"` object
  with integer `"code"` and string `"message"`.
- **SPEC-0050-R02** The `"id"` field of an `/mcp/` error response MUST match the
  request's JSON-RPC `id` if the body was successfully parsed as a JSON-RPC
  request with an `id`. For `GET` and `DELETE` requests, JSON-RPC notifications
  (requests without an `"id"`), JSON-RPC batches (array bodies), or whenever the
  body could not be parsed as a JSON-RPC request, `"id"` MUST be `null`.
- **SPEC-0050-R03** Invalid client requests (disallowed browser `Origin`, disallowed
  HTTP methods, request path containing `.` or `..` segments, `POST` requests
  without `Content-Type: application/json`, `GET` or `DELETE` requests carrying a body,
  failures reading the request body, JSON-RPC batches (array bodies, per SPEC-0018-R03),
  invalid JSON-RPC method names, or access tokens sent outside the `Authorization`
  header) MUST return JSON-RPC error code `-32600` (`Invalid Request`).
- **SPEC-0050-R04** Requests with invalid or malformed parameter structures
  (including `tools/call` with an invalid, non-string, or duplicate `params.name`,
  refining the `-32600` assignment in SPEC-0044-R05) MUST return JSON-RPC error code
  `-32602` (`Invalid params`) with HTTP status `400`.
- **SPEC-0050-R05** Client authentication refusals on `/mcp/` routes (`writeRefusal`)
  MUST return a JSON-RPC 2.0 error body with `"id": null` and error codes in the
  JSON-RPC reserved server error range:
  - Unauthenticated (missing, expired, or invalid token): code `-32007` (HTTP `401`)
  - Forbidden (insufficient scope): code `-32008` (HTTP `403`)
  - Authorization server unavailable: code `-32009` (HTTP `503`)
  The Gateway MUST preserve all security and transport headers on these responses,
  including `WWW-Authenticate`, `Cache-Control: no-store`, and `Retry-After`.
- **SPEC-0050-R06** Requests targeting an unregistered server route or an
  unforwarded subpath MUST return JSON-RPC error code `-32010` 
  (`Route not found`) with HTTP status `404`. (Code `-32010` in the reserved
  server error range is used instead of `-32601` so MCP clients can
  distinguish an infrastructure routing failure from an unimplemented JSON-RPC method).
- **SPEC-0050-R07** Gateway and upstream infrastructure failures MUST return
  error codes in the JSON-RPC reserved server error range (`-32000` to
  `-32099`):
  - Client request body timeout: code `-32006` (HTTP `408`)
  - Session not found: code `-32002` (HTTP `404`)
  - Upstream timeout: code `-32003` (HTTP `504`)
  - Upstream credential unavailable: code `-32004` (HTTP `503`)
  - Audit queue full: code `-32005` (HTTP `503`)
  - Upstream request failure or unsupported redirect: code `-32000` (HTTP `502`)
- **SPEC-0050-R08** Existing HTTP status codes for all `/mcp/` error responses
  MUST be preserved.
- **SPEC-0050-R09** Error responses on non-MCP routes (`/v1/*`, `/ui/`,
  `/healthz`, `/readyz`, `/metrics`) MUST NOT return JSON-RPC error objects.

## Acceptance criteria

- Given an `/mcp/` request to an unregistered route (e.g. `/mcp/ns.nope`),
  when sent by a client, then the response has HTTP status `404` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32010,"message":"MCP server \"ns.nope\" is not registered"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R06, SPEC-0050-R08).
- Given a valid JSON-RPC `POST` to an `/mcp/` route with `id: 42` that times out
  waiting for upstream, when the request timeout expires, then the response has
  HTTP status `504` and body `{"jsonrpc":"2.0","id":42,"error":{"code":-32003,"message":"upstream request timed out"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R07, SPEC-0050-R08).
- Given an `/mcp/` request with a disallowed browser `Origin`, when evaluated by
  the Gateway, then the response has HTTP status `403` and a JSON-RPC error
  body with `"id": null`, code `-32600`, and a message explaining that the
  origin is not allowed (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R03, SPEC-0050-R08).
- Given a `tools/call` `POST` request with an invalid tool name (e.g. `"delete_all "`),
  when checked by the Gateway, then the response has HTTP status `400` and body
  `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"the tool name must be 1 to 128 characters of A-Z, a-z, 0-9, _, ., /, and -"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R04, SPEC-0050-R08).
- Given client authentication enabled, when a request is sent to an `/mcp/` route
  without an Authorization header, then the response has HTTP status `401`,
  header `WWW-Authenticate` containing the challenge, and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32007,"message":"login required"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R05, SPEC-0050-R08).
- Given a `POST` request to an `/mcp/` route with `Content-Type: text/plain`,
  when checked by the Gateway, then the response has HTTP status `415` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"MCP requests must be POSTed with Content-Type: application/json"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R03, SPEC-0050-R08).
- Given a `POST` request to an `/mcp/` route whose body does not arrive within the
  request timeout, then the response has HTTP status `408` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32006,"message":"the request body did not arrive within the request timeout"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R07, SPEC-0050-R08).
- Given a `GET` event stream request to an `/mcp/` route without an Authorization header,
  when client authentication is enabled, then the response has HTTP status `401`,
  header `WWW-Authenticate`, and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32007,"message":"login required"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R05, SPEC-0050-R08).
- Given a `DELETE` session request targeting an unknown session on an `/mcp/` route,
  when rejected by the Gateway, then the response has HTTP status `404` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32002,"message":"session not found"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R07, SPEC-0050-R08).
- Given an `/mcp/` notification `POST` request without an `id` (e.g. `{"jsonrpc":"2.0","method":"notifications/initialized"}`) sent to an unregistered route,
  then the response has HTTP status `404` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32010,"message":"MCP server \"ns.nope\" is not registered"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R06, SPEC-0050-R08).
- Given a JSON-RPC batch request (a JSON array body `[...]`), when evaluated by
  the Gateway, then the response has HTTP status `400` and body
  `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"JSON-RPC batches are not supported; send one JSON-RPC message per request"}}` (SPEC-0050-R01, SPEC-0050-R02, SPEC-0050-R03, SPEC-0050-R08).
- Given a request to `/v1/servers` without credentials, when rejected, then the
  response is a standard HTTP `401` with plain JSON `{"error":"login required"}` and
  does not contain `"jsonrpc"` (SPEC-0050-R09).

## Security and privacy

Error messages returned in the JSON-RPC error object must not expose internal
network topologies, backend pod IP addresses, or secrets. Error messages remain
bounded to existing sanitized messages already returned by the Gateway.

## Compatibility

This change affects MCP clients communicating over Streamable HTTP on `/mcp/`
routes. Clients that parse JSON-RPC error bodies will now receive actionable
error structures instead of unhandled transport errors. It refines SPEC-0044-R05
by returning standard JSON-RPC 2.0 error code `-32602` (`Invalid params`)
instead of `-32600` for invalid `tools/call` `params.name`. HTTP status codes
and audit event records remain unchanged. No CRD changes or Helm value changes
are required.

## Documentation

Update Gateway component documentation to document JSON-RPC error codes returned
on `/mcp/` routes.

## Open questions

- None.
