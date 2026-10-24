# SPEC-0006: MCP client authentication

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-23 |
| Issue | [#38](https://github.com/kavrynt/kavrynt/issues/38) |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("The gateway is an OAuth 2.1 resource server", "No token passthrough") |
| Supersedes | none |

## Summary

The Gateway becomes an OAuth 2.1 resource server for MCP clients: when
authentication is enabled, every request to `/mcp/<route>` must carry a
bearer access token issued by an authorization server the installation
trusts (the customer's IdP, never Kavrynt Cloud), with the route as its
audience. Agents that present no token, or a wrong one, are refused with the
responses the MCP authorization specification expects, so standard MCP
clients can discover where to get a token. It is opt-in until
`1.0.0-beta.1`.

## Problem

Anyone who can reach the Gateway can call every routed tool, including tools
that change state. The console login (SPEC-0005) protects only the console.
Platform teams cannot expose a Gateway beyond a trusted network, and nothing
downstream (policy, audit, approvals in ADR-0003) can work without a
verified caller identity. See issue #38.

## Goals

- Only callers with a valid token for the route reach an MCP server.
- Standard MCP clients can find the authorization server on their own
  (Protected Resource Metadata) and get a clear reason when refused.
- A token issued for one route, or for anything else, is not accepted.
- Existing installs keep working until an operator turns authentication on.

## Non-goals

- Deciding which caller may use which tool: policy (ADR-0003, own spec).
  This spec only checks that the token is valid for the route and carries
  the configured scopes.
- Upstream token exchange and static upstream credentials (own spec).
- Audit events and recording caller identity in traffic (own spec).
- Approvals.
- Opaque tokens and token introspection (RFC 7662); only JWT access tokens.
- Signing the console in through the authorization server (authorization
  code with PKCE); the console keeps its admin login (SPEC-0005).
- Client registration (Client ID Metadata Documents, Dynamic Client
  Registration): that is between the client and the authorization server.
- Turning authentication on by default; that happens in `1.0.0-beta.1`.

## Requirements

### Enabling

- **SPEC-0006-R01** Authentication MUST be off unless enabled
  (`gateway.auth.enabled`, flag `--auth`). When off, `/mcp/` routes MUST
  behave as before this spec.
- **SPEC-0006-R02** When enabled, the Gateway MUST refuse to start without
  an external base URL (`gateway.auth.resourceURL`) and at least one
  trusted issuer, and the chart MUST reject such values at install time.

### Refusing requests

- **SPEC-0006-R03** When enabled, a request to `/mcp/<route>` without an
  `Authorization: Bearer` header MUST get `401` with
  `WWW-Authenticate: Bearer resource_metadata="<metadata URL of the route>"`
  and, when scopes are configured, `scope="<required scopes>"`. It MUST NOT
  reach the MCP server.
- **SPEC-0006-R04** A request carrying a token anywhere other than the
  `Authorization` header (for example an `access_token` query parameter or
  form field) MUST get `400` with `error="invalid_request"` and MUST NOT
  reach the MCP server.
- **SPEC-0006-R05** A token that fails validation (R07) MUST get `401` with
  `error="invalid_token"`; the response MUST NOT say which check failed.
- **SPEC-0006-R06** A valid token that lacks a configured required scope
  MUST get `403` with `error="insufficient_scope"` and the required
  `scope`.

### Protected Resource Metadata

- **SPEC-0006-R07** The Gateway MUST serve RFC 9728 metadata for each routed
  server at `/.well-known/oauth-protected-resource/mcp/<route>`, without
  authentication, with `resource` set to the route's canonical URI
  (`<resourceURL>/mcp/<route>`), `authorization_servers` listing the
  trusted issuers, `bearer_methods_supported: ["header"]`, and
  `scopes_supported` when scopes are configured. Unknown routes MUST get
  `404`.

### Validating tokens

- **SPEC-0006-R08** A token MUST be accepted only when all of these hold: it
  is a signed JWT whose algorithm is asymmetric and allow-listed (no `none`,
  no HMAC); `iss` exactly equals a trusted issuer; the signature verifies
  with a key of that issuer's JWKS selected by `kid`; `exp` is present and
  in the future and `nbf`, when present, is in the past, each with at most
  60 seconds of clock skew; and `aud` contains the route's canonical URI
  (RFC 8707).
- **SPEC-0006-R09** A token whose audience is another route, the Gateway's
  base URL alone, or any other resource MUST be refused (R05).

### Keys

- **SPEC-0006-R10** The Gateway MUST obtain each issuer's JWKS from its
  configured `jwksURI`, or else from its authorization server metadata
  (RFC 8414, then OpenID Connect discovery). It MUST cache keys, refresh
  them periodically, and fetch again (at most once a minute per issuer)
  when a token names an unknown `kid`.
- **SPEC-0006-R11** Metadata and JWKS fetches MUST use HTTPS (except as
  R20 allows), MUST be bounded in time and size, and MUST follow the Gateway's egress policy
  (`internal/egress`): no cloud metadata, link-local, or loopback addresses.
- **SPEC-0006-R12** When an issuer's keys cannot be loaded, requests with
  that issuer's tokens MUST fail closed with `503`; other issuers MUST keep
  working, and `/readyz` MUST NOT change.

### No passthrough, no leaks

- **SPEC-0006-R13** The caller's `Authorization` header MUST still be
  removed before the request is forwarded (ADR-0003; existing behavior).
- **SPEC-0006-R14** Tokens and their claims MUST NOT appear in logs,
  metrics, `/v1/traffic`, or responses. Refusals MUST be recorded as
  traffic outcomes (`unauthenticated` for R03–R05, `forbidden` for R06,
  `auth_unavailable` for R12) with the same metadata as other calls.

### Console

- **SPEC-0006-R15** `GET /v1/session` MUST report whether MCP client
  authentication is enabled.
- **SPEC-0006-R16** When it is enabled, the Playground MUST accept an
  access token, keep it only in the page's memory, send it only in the
  `Authorization` header of its own `/mcp/` requests, and explain a `401`,
  `403`, or `503` answer.
- **SPEC-0006-R17** When it is enabled, the console notice
  (SPEC-0005-R16) MUST say that MCP routes require tokens instead of saying
  they are unauthenticated.

### Route listing

- **SPEC-0006-R18** `GET /v1/routes` MUST NOT include the userinfo, query
  string, or fragment of any endpoint, whether or not authentication is
  enabled.
- **SPEC-0006-R19** When authentication is enabled, `GET /v1/routes` MUST
  list route names, versions, and transports only, without endpoints: an
  unauthenticated caller must not learn internal service addresses.

### Issuer transport

- **SPEC-0006-R20** An issuer whose metadata or JWKS URL is `http` MUST be
  refused at start-up and by the chart, unless that issuer sets
  `allowHTTP: true`; the Gateway MUST log a warning naming the issuer (not
  any key or token) when it is set.

## Acceptance criteria

- Given `gateway.auth.enabled=true` with a trusted issuer, when an agent
  calls `/mcp/team-a.search` without a token, then it gets `401` with a
  `resource_metadata` URL, and that URL returns the issuer and the
  resource `https://mcp.example.com/mcp/team-a.search` (R03, R07).
- Given a token from that issuer with audience
  `https://mcp.example.com/mcp/team-a.search`, when it calls that route,
  then the call is proxied and the MCP server receives no `Authorization`
  header (R08, R13).
- Given the same token, when it calls `/mcp/team-b.billing`, then it gets
  `401` (R09).
- Given a token signed with `HS256`, or `alg: none`, or an expired token,
  then `401` (R08).
- Given a required scope `mcp:tools`, when a token without it calls, then
  `403 insufficient_scope` (R06).
- Given an issuer whose JWKS endpoint is down, when its tokens arrive, then
  `503`, and tokens of another trusted issuer still work (R12).
- Given authentication is off, when an agent calls without a token, then
  the call is proxied as before (R01).

## Security and privacy

Tokens are untrusted input until validated; validation fails closed. Only
asymmetric algorithms are accepted, so a JWKS public key cannot be abused as
an HMAC secret. The audience check (RFC 8707) stops a token minted for
another MCP server, or another route, from being replayed here. Key fetching
is an outbound request to an operator-configured URL; it keeps the egress
policy so a misconfigured issuer cannot reach cloud metadata. Tokens are
never forwarded upstream (no passthrough) and never logged or recorded;
refusals are visible only as outcomes. The Playground holds a pasted token
in memory only (SPEC-0002-R13 storage scan applies).

The console admin login and MCP client authentication stay separate: the
admin session never authorizes `/mcp/` calls.

## Compatibility

Opt-in: no behavior changes until `gateway.auth.enabled=true`. New Helm
values under `gateway.auth` (`enabled`, `resourceURL`, `issuers` with
`issuer`, optional `jwksURI` and `allowHTTP`, `requiredScopes`) with schema validation,
and matching Gateway flags. New outcomes appear in metrics and the console.
New Go dependency for JWS and JWKS handling (proposed:
`github.com/go-jose/go-jose/v4`), which must pass govulncheck and Trivy. No
CRD change.

When enabled, every agent needs a token from the trusted authorization
server, and the console Playground needs one pasted in.

## Documentation

When `Implemented`: Gateway component doc (authentication section,
endpoints, flags, outcomes), chart README values and security notes,
README security posture, SECURITY.md, and a guide for configuring a common
authorization server. CLAUDE.md "What works today" changes once this ships.

## Decisions

- HTTP issuers: HTTPS is required; an issuer may opt in to `http` with
  `allowHTTP: true` for in-cluster authorization servers (R20).
- Unavailable keys answer `503` with `Retry-After` (R12): the token may be
  valid, so `401` would send clients to the authorization server for
  nothing.
- Required scopes stay installation-wide. Per-server and per-tool rules
  belong to the policy spec (SPEC-0012); no CRD change here.
- The traffic record carries no caller identity. Identity is recorded by
  audit events (SPEC-0013).
- `/v1/routes` no longer exposes endpoints once authentication is on, and
  never exposes credentials in an endpoint (R18, R19).
