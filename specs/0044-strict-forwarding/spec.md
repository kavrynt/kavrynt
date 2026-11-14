# SPEC-0044: Only MCP requests and answers pass the Gateway

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-11-13 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

The Gateway forwards a request only when it can see everything the MCP
server will act on, and gives a client only the parts of the server's
answer that MCP needs. A body on `GET` or `DELETE`, a `POST` that is not
JSON, a method or tool name with characters a rule cannot match, a
redirect, and response headers that act on the Gateway's origin no longer
pass.

## Problem

A security test of the Gateway found three ways around what it enforces.

- **Requests the policy and the audit do not see.** The Gateway reads the
  JSON-RPC message of a `POST` only. A `tools/call` body sent with `GET` or
  `DELETE` was forwarded with an `X-HTTP-Method-Override: POST` header. The
  policy decided on a request without an MCP method, and the audit event
  named none. A server that honours the override header, or reads a body on
  `GET`, runs a call nobody decided on.
- **Names a rule does not match.** With a policy that denies the tool
  `delete_all`, calls to `"delete_all "`, `"delete_all\u0000"`, and the
  method `"tools/call "` were allowed. A server that trims or cuts such
  names runs the denied tool.
- **Answers that act on the Gateway.** Every route shares the Gateway's
  origin and its place in the client's OAuth flow. A registered server's
  `WWW-Authenticate` header (naming another authorization server),
  `Strict-Transport-Security: max-age=0`, `Clear-Site-Data`, `Refresh`, and
  a `307` to another route or host all reached the client as if the Gateway
  had sent them. Whoever registers a server in one namespace can use them
  against the callers of every route.

A fourth finding belongs here because the same check closes it: with
client authentication on, the Gateway reads up to 1 MiB of a form body
before it has checked the token, looking for an `access_token` field
(SPEC-0006-R04). Callers without a token can make it hold that memory.

## Goals

- What the policy decides on and the audit records is what the server
  receives.
- A registered MCP server cannot act on the Gateway's origin or on the
  client's sign-in through its answer.
- No request body is read before authentication.

## Non-goals

- Folding names. `DELETE_ALL` and `delete_all` stay different tools. A
  policy that must hold against a server that folds names denies tools that
  were not discovered (`tool_discovered`, SPEC-0028).
- The `X-Forwarded-For`, `Forwarded`, and `X-Real-IP` headers a client
  sends. They still reach the server unchanged; see the open questions.
- The status of a server's own `401` or `403`. It still reaches the client,
  without `WWW-Authenticate`.
- Checking a server's answer body, or limiting its size.
- The content type of an answer. Answers already render in a sandbox.

## Requirements

### Requests

- **SPEC-0044-R01** The Gateway MUST answer `400` to a `GET` or `DELETE`
  request to an `/mcp/` route that has a body (a `Content-Length` other
  than `0`, or a body without one). It MUST NOT read the body and MUST NOT
  contact the MCP server. A `Content-Length` above the request size limit
  gets `413` from SPEC-0038-R01 instead.
- **SPEC-0044-R02** The Gateway MUST answer `415` to a `POST` request to an
  `/mcp/` route whose `Content-Type` is missing or is not
  `application/json` (parameters such as `charset` are allowed). It MUST
  NOT read the body and MUST NOT contact the MCP server.
- **SPEC-0044-R03** The checks of R01 and R02 MUST run before
  authentication and answer the same for registered and unregistered
  routes, so the Gateway reads no body of a caller it has not
  authenticated, and the answer tells a caller nothing about a route.
- **SPEC-0044-R04** The Gateway MUST NOT forward the request headers
  `X-HTTP-Method-Override`, `X-HTTP-Method`, and `X-Method-Override`.
- **SPEC-0044-R05** The Gateway MUST answer `400`, as a JSON-RPC error with
  code `-32600`, to a `POST` whose JSON-RPC `method`, or whose `tools/call`
  `params.name`, is not 1 to 128 bytes of `A-Z`, `a-z`, `0-9`, `_`, `.`,
  `/`, and `-`. It MUST NOT contact the MCP server. This is the rule the
  Operator already applies to the tool names it records. A message without
  a `method` (a client's JSON-RPC response) is not checked.
- **SPEC-0044-R06** R05 MUST apply whenever the tool authorization policy
  is on. Without a policy it MUST apply unless
  `gateway.config.strictProtocol` is `false`.

### Answers

- **SPEC-0044-R07** The Gateway MUST answer `502` with a fixed message when
  the MCP server answers with a `3xx` status. The client MUST NOT receive
  the server's `Location` or body.
- **SPEC-0044-R08** Of the MCP server's response headers, the Gateway MUST
  forward only `Allow`, `Cache-Control`, `Content-Encoding`,
  `Content-Language`, `Content-Length`, `Content-Type`, `Date`,
  `MCP-Protocol-Version`, `Mcp-Session-Id`, `Retry-After`, and `Vary`, and
  the headers named by R09.
- **SPEC-0044-R09** The chart value `gateway.config.allowResponseHeaders`
  (Gateway flag `--allow-response-headers`), empty by default, MUST add
  header names to R08. The chart and the Gateway MUST refuse a name in this
  list: `Set-Cookie`, `WWW-Authenticate`, `Proxy-Authenticate`, `Location`,
  `Refresh`, `Strict-Transport-Security`, `Clear-Site-Data`,
  `Content-Security-Policy`, `Content-Security-Policy-Report-Only`,
  `X-Content-Type-Options`, `Alt-Svc`, `Server`, `X-Powered-By`,
  `X-Request-Id`, and every name that starts with `Access-Control-`.

### Records

- **SPEC-0044-R10** A request refused by R01, R02, or R05 MUST be counted
  with outcome `protocol_error` when its route is registered, and its audit
  event MUST have decision `invalid` and a reason that names the check. An
  answer refused by R07 MUST be counted with outcome `upstream_error` and
  status `502`.

## Acceptance criteria

- Given a policy that denies `delete_all`, when a client sends `GET` with a
  `tools/call` body for it and `X-HTTP-Method-Override: POST`, then it gets
  `400` and the server receives nothing (R01). The same request without the
  body reaches the server without the override header (R04).
- Given client authentication on, when a caller without a token sends a
  `POST` with `Content-Type: application/x-www-form-urlencoded` and a body
  that never ends, then it gets `415` at once, for a registered route and
  for an unregistered one, and the Gateway reads none of the body (R02,
  R03).
- Given a policy that denies `delete_all`, when a client calls the tool
  `"delete_all "` or the method `"tools/call "`, then it gets `400` with a
  JSON-RPC error, with `strictProtocol` on or off (R05, R06).
- Given no policy and `strictProtocol: false`, when a client calls a tool
  whose name has a space, then the call is forwarded (R06).
- Given a server that answers `307` with a `Location`, when a client calls
  it, then the client gets `502` and no `Location`, and the call shows as
  `upstream_error` (R07, R10).
- Given a server that answers with `WWW-Authenticate`,
  `Strict-Transport-Security`, `Clear-Site-Data`, `Refresh`, and
  `X-Custom`, then the client gets none of them, and still gets
  `Content-Type` and `Mcp-Session-Id` (R08).
- Given `gateway.config.allowResponseHeaders: [X-Custom]`, then the client
  gets `X-Custom`; given `[WWW-Authenticate]`, then the install fails
  (R09).
- Given any request refused by R01, R02, or R05 on a registered route, then
  the metrics, the console traffic view, and the audit event show it as a
  protocol error (R10).

## Security and privacy

The input is untrusted on both sides: requests from clients, and answers
from MCP servers that any namespace owner can register.

- R01, R04, and R05 close the gap between the message the policy decides on
  and the message the server runs. They fail closed: what the Gateway
  cannot read as MCP is refused, not forwarded.
- R02 and R03 remove the only body the Gateway read before authentication.
  A form body is no longer read at all, so a token in a form field can
  never be accepted.
- R07 to R09 keep a registered server from sending clients to another
  authorization server, to another route or host, or from changing how
  browsers treat the Gateway's origin. The list in R09 cannot be opened by
  configuration.
- Refusals carry no request data. Nothing new is logged or stored.

Known limit: the Gateway cannot know how a server reads a name it accepts.
R05 removes the names no rule can be written for; case differences are
left to `tool_discovered`.

## Compatibility

New behavior in the next release, on by default. No `MCPServer` API or CRD
change. One new chart value and Gateway flag (R09). Release notes need to
say:

- A server that answers with a redirect (for example from `/mcp` to
  `/mcp/`) now fails with `502`. Register the final URL.
- Response headers outside the list in R08 no longer reach clients. Add the
  ones a client needs to `gateway.config.allowResponseHeaders`.
- A `POST` that is not `application/json`, and a `GET` or `DELETE` with a
  body, are refused. This includes sub-paths of routes with
  `spec.forwardSubpaths`.
- A tool whose name has characters outside R05 can no longer be called.
  The Operator never recorded such tools.
- A token in a form field now gets `415`, not the `400` of SPEC-0006-R04,
  which is withdrawn; SPEC-0006-R21 keeps the `400` for query tokens.
- `TestProxyPassesRedirectsToClient` and the sentence "the Gateway passes
  redirects to the client" describe the behavior R07 replaces.

## Documentation

`docs/components/gateway/README.md` (forwarding rules, response headers,
the client authentication answers, outcomes, the new flag), the chart
README and `values.yaml`, `ROADMAP.md`, and "What Works Today" in
`CLAUDE.md`.

## Decisions

Settled by the owner on 2026-11-13, following the recommendations of the
draft.

- **Form bodies get `415`.** Every `POST` that is not JSON is refused the
  same way. SPEC-0006-R04 is withdrawn and replaced by SPEC-0006-R21 for
  query tokens, so no answer tells a client that its token was in a body.
- **R02 applies to sub-paths of `forwardSubpaths` routes.** A sub-path that
  takes a form or an upload is not MCP; with the policy on, such a `POST`
  was already refused (SPEC-0012-R09).
- **Without a policy, R05 follows `strictProtocol`**, so a team with an
  unusual tool name has a way out that does not weaken a policy.
- **`allowResponseHeaders` is kept.** A fixed list will be wrong for
  someone, and the names that matter cannot be added.
- **Client address headers** (`X-Forwarded-For`, `Forwarded`,
  `X-Real-IP`) are left to a separate spec, because the Gateway needs the
  trusted-proxy setting that today exists only for the console (SPEC-0030).
- **A server's own `401` still reaches the client**, without
  `WWW-Authenticate`. Whether it should become `502` is decided later, with
  evidence from MCP clients.
