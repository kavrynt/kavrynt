# SPEC-0015: Upstream token exchange

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("No token passthrough": token exchange) |
| Supersedes | none |

## Summary

For an MCP server that needs a token on behalf of the caller, the Gateway
exchanges the caller's verified access token (SPEC-0006) for a token whose
audience is that server, using OAuth 2.0 Token Exchange (RFC 8693) at the
caller's authorization server, and sends only the exchanged token upstream.
The server learns who the user is without ever seeing the token issued for
the Gateway.

## Problem

Static upstream credentials (SPEC-0011) identify the Gateway, not the user.
MCP servers that enforce per-user access (ticketing, document stores) need a
user-scoped token, and passing the caller's token through is forbidden by
the MCP authorization specification.

## Goals

- Per-user upstream tokens without token passthrough.
- The exchanged token preserves the user and names the acting agent where
  the authorization server supports it.
- Exchange failures fail closed and never leak tokens.

## Non-goals

- Exchange without client authentication (there is no subject token).
- Enterprise-Managed Authorization (ID-JAG) flows; own spec.
- Refresh tokens: exchanged tokens are short-lived and re-exchanged.

## Requirements

### API and configuration

- **SPEC-0015-R01** `MCPServer.spec.tokenExchange` MUST be optional, with
  `audience` (required, absolute URI) and optional `scopes`, and MUST be
  mutually exclusive with `spec.credential`; the CRD and validation MUST
  reject both together and `tokenExchange` on `stdio` servers.
- **SPEC-0015-R02** Each trusted issuer (SPEC-0006) MAY name a token
  endpoint (else discovered from its metadata) and the Gateway's client
  credentials, read from a kubelet-mounted Secret; the Gateway MUST NOT get
  Secret RBAC.

### Exchange

- **SPEC-0015-R03** For a server with `tokenExchange`, the Gateway MUST send
  an RFC 8693 request to the caller's issuer with `subject_token` = the
  caller's token, `subject_token_type`
  `urn:ietf:params:oauth:token-type:access_token`, the configured
  `audience` and `scope`, authenticated with the Gateway's client
  credentials, and MUST forward only the returned `access_token` as
  `Authorization: Bearer`.
- **SPEC-0015-R04** The Gateway MUST cache exchanged tokens per subject
  token (by SHA-256) and audience until 30 seconds before `expires_in`, and
  MUST bound the cache size.
- **SPEC-0015-R05** Token endpoint requests MUST use HTTPS (unless the
  issuer allows HTTP), be bounded in time and size, and follow the egress
  guard.
- **SPEC-0015-R06** When the exchange fails, the request MUST get `502` (or
  `503` when the endpoint is unreachable) with a fixed message, outcome
  `exchange_failed`, and MUST NOT reach the MCP server.
- **SPEC-0015-R07** A server with `tokenExchange` and client authentication
  off MUST answer `503` and the Gateway MUST log the misconfiguration once.

### Never disclosed

- **SPEC-0015-R08** Subject tokens, exchanged tokens, and client secrets MUST
  NOT appear in logs, metrics, traffic, audit events, status, or responses.

### End to end

- **SPEC-0015-R09** With the e2e test authorization server extended with a
  token endpoint, a server requiring a token for its own audience MUST be
  callable through the Gateway with an exchanged token, and MUST NOT
  receive the caller's token.

## Acceptance criteria

- Given `tokenExchange: {audience: https://tickets.example.com}` and a
  caller token for the route, then the server receives a token with
  audience `https://tickets.example.com` and the caller's `sub` (R03).
- Given the authorization server rejects the exchange, then `502` and
  outcome `exchange_failed` (R06).
- Given two calls with the same caller token within the token lifetime, then
  one exchange request is made (R04).

## Security and privacy

The Gateway becomes an OAuth client with its own credentials; a leaked
client secret lets an attacker exchange tokens it already holds, so the
secret is mounted, never logged, and rotated by Secret update. The cache
stores tokens in memory only, keyed by hash. The authorization server
decides whether the Gateway may exchange for an audience.

## Compatibility

Additive CRD field (both copies), new issuer settings and Secret values, new
outcome. No change for servers without `tokenExchange`.

## Documentation

Gateway component doc (token exchange), chart README, examples, README
security posture, CLAUDE.md.

## Open questions

- Should the Gateway also support `private_key_jwt` client authentication
  instead of a client secret?
- How should `act` (actor) be requested: from the token's `client_id`, or
  an explicit `actor_token`?
