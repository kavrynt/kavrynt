# SPEC-0036: Policy denials that MCP clients can read

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-31 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | SPEC-0012-R08 and SPEC-0012-R12, only their `403` for denied JSON-RPC requests |

## Summary

When the tool authorization policy denies a JSON-RPC request, the Gateway
answers `200` with the JSON-RPC error that names the policy's reason, as an
MCP server answers a request it refuses. MCP client SDKs then hand the
agent the reason instead of failing the transport.

## Problem

SPEC-0012-R08 answers a denied request with `403` and a JSON-RPC error
body. MCP Streamable HTTP clients treat a non-2xx answer to a request as a
transport failure: the official Python SDK raises `HTTPStatusError: 403
Forbidden` and never reads the body. In a real-environment test, an agent
refused a refund could not see `denied by policy: destructive tools are for
ops-agent only`, so neither it nor its model could tell a policy decision
from a broken connection. The reason string exists for the agent, and the
transport status hides it.

## Goals

- MCP clients surface a denied request as a JSON-RPC error with the
  policy's reason.
- Audit events, metrics, and the console still show the denial as a
  denial.

## Non-goals

- Turning denials into tool results (`isError: true`): a denial is a
  protocol-level refusal, not a tool's output, and applies to every method.
- Changing denials of requests that carry no JSON-RPC request
  (notifications, responses, `GET`, `DELETE`): they have no response to
  hold an error.
- Changing authentication refusals (`401`, `403` for scopes; SPEC-0006),
  which OAuth clients need as HTTP statuses.

## Requirements

- **SPEC-0036-R01** A denied `POST` whose body is a JSON-RPC request (it has
  a `method` and an `id`) MUST get `200` with `Content-Type:
  application/json` and a JSON-RPC error response with that `id`, code
  `-32001`, and the message of SPEC-0012-R08 (`denied by policy`, then the
  bounded reason).
- **SPEC-0036-R02** A denied `POST` whose body is a notification or a
  response, and a denied `GET` or `DELETE`, MUST still get `403`, as
  SPEC-0012-R08 says.
- **SPEC-0036-R03** A denied request MUST NOT reach the MCP server, MUST be
  recorded with outcome `denied` and decision `deny`, and its audit event
  MUST record the HTTP status sent.

## Acceptance criteria

- Given a policy that denies `refund_order` to `research-agent`, when that
  agent calls it with the official MCP Python SDK, then the SDK raises an
  `McpError` whose message is `denied by policy: <reason>`, and the server
  received nothing (R01, R03).
- Given the same denial sent with `curl`, then the answer is `200` with
  `{"jsonrpc":"2.0","id":9,"error":{"code":-32001,"message":"denied by
  policy: ..."}}` (R01).
- Given a policy that denies a notification or an event-stream `GET`, then
  the answer is `403` (R02).
- Given any denial, then its audit event has decision `deny` and the status
  that was sent, and the traffic outcome is `denied` (R03).

## Security and privacy

The answer is still a refusal: the request never reaches the server, and
the body holds only what SPEC-0012-R08 already returned. Code that
recognized denials by the HTTP status alone must now read the JSON-RPC
error; the audit `decision` and the `denied` outcome stay the authoritative
record.

## Compatibility

A behavior change for clients and scripts that check for `403` on a denied
`tools/call`. They get `200` with the same JSON-RPC error body. Release
notes say so. No Helm values, CRD, or audit format change.

## Documentation

Gateway component docs (Tool authorization policy, outcomes),
`docs/TESTING.md` step 6, `CLAUDE.md`.

## Open questions

None: decided with the test results (see [plan.md](plan.md)).
