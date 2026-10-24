# SPEC-0011: Upstream credentials

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("No token passthrough": static upstream credentials) |
| Supersedes | none |

## Summary

An `MCPServer` can declare that its upstream needs a credential. A platform
administrator stores that credential in one Secret in the Kavrynt namespace,
keyed by the server's namespace and name and pinned to its endpoint's
origin. The Gateway adds it to every proxied request, and the Operator uses
it for tool discovery. Neither component gains RBAC on Secrets, and no team
can redirect another team's credential to an endpoint of its choice.

## Problem

The Gateway removes the caller's `Authorization` header (no token
passthrough), so an MCP server that requires its own API key or token
cannot be routed at all today. Teams either leave such servers unprotected
or bypass the Gateway. ADR-0003 allows static upstream credentials from
Secrets when token exchange is not available.

## Goals

- Route MCP servers that require a static API key or bearer token.
- Keep the Gateway's least-privilege RBAC (no Secret access) and the
  end-to-end contract that proves it.
- A credential reaches only the endpoint it was issued for.

## Non-goals

- OAuth 2.0 Token Exchange (RFC 8693) on behalf of the caller; it needs
  client authentication (SPEC-0006) and its own spec.
- Per-team self-service Secrets in the team's namespace (would need Secret
  RBAC for the Gateway).
- Mutual TLS client certificates to upstreams.
- Credentials for `stdio` servers (not proxied).

## Requirements

### API

- **SPEC-0011-R01** `MCPServer.spec.credential` MUST be optional, with
  fields `header` (default `Authorization`) and `scheme` (optional, for
  example `Bearer`). The CRD and `kavryctl validate` MUST reject a `header`
  that is not a valid HTTP field name or is one of `Host`,
  `Content-Length`, `Transfer-Encoding`, `Connection`, `Cookie`,
  `X-Kavrynt-Route`, or a `Mcp-*` header, and MUST reject `credential` on a
  `stdio` server.

### Storage and binding

- **SPEC-0011-R02** The Gateway and the Operator MUST read credentials only
  from the Secret named by `global.upstreamCredentials.secretName`, mounted
  by the kubelet; neither MUST gain any RBAC rule on Secrets.
- **SPEC-0011-R03** The credential for an `MCPServer` MUST be the Secret key
  `<namespace>.<name>`, and it MUST be used only when the key
  `<namespace>.<name>.origin` equals the origin (`scheme://host:port`,
  default port made explicit) of the server's current `spec.endpoint`.
- **SPEC-0011-R04** A changed Secret MUST take effect without a restart,
  once the kubelet updates the mount.

### Gateway

- **SPEC-0011-R05** For a route whose server declares `credential`, the
  Gateway MUST remove any client-sent header of the same name and send the
  credential as `<header>: [<scheme> ]<value>`.
- **SPEC-0011-R06** When the credential is missing, empty, or its origin
  pin does not match, the Gateway MUST answer `503` with
  `upstream credential unavailable`, MUST NOT forward the request, and MUST
  record outcome `credential_unavailable`.
- **SPEC-0011-R07** `/v1/servers` MUST report, per server, whether a
  credential is required and whether it is available, and MUST NOT include
  the value or the pin.

### Operator

- **SPEC-0011-R08** Tool discovery MUST send the credential the same way
  (R03, R05) when the server declares one; when it is unavailable,
  `ToolsDiscovered` MUST be `False` with reason `CredentialUnavailable` and
  `Ready` MUST NOT change.

### Never disclosed

- **SPEC-0011-R09** Credential values MUST NOT appear in logs, metrics,
  `/v1/traffic`, `/v1/routes`, the console, `MCPServer` status, events, or
  error messages, and MUST NOT be sent to Kavrynt Cloud.

### End to end

- **SPEC-0011-R10** With a test MCP server that requires a bearer token,
  calls through the Gateway MUST succeed when the Secret holds the token
  and pin, MUST get `503` without it, and `status.tools` MUST be populated
  only when it is present; the Gateway's RBAC check MUST still pass.

## Acceptance criteria

- Given `team-a/search` with `credential: {scheme: Bearer}` and the Secret
  keys `team-a.search` and `team-a.search.origin =
  http://search.team-a.svc:8080`, when an agent calls `/mcp/team-a.search`
  with its own `Authorization`, then the server receives `Authorization:
  Bearer <stored value>` and never the agent's header (R03, R05).
- Given someone changes `team-a/search`'s endpoint to
  `http://evil.example:80`, then the Gateway answers `503` and the stored
  credential is not sent anywhere (R03, R06).
- Given the Secret lacks `team-a.search`, then `503`, outcome
  `credential_unavailable`, and `ToolsDiscovered=False` with
  `CredentialUnavailable` while `Ready` stays `True` (R06, R08).
- Given the token is rotated in the Secret, then new calls carry the new
  value without a restart (R04).
- Given `header: Cookie`, then the CRD rejects the object (R01).

## Security and privacy

The credential binding uses the route identity (namespace and name, which
only users allowed to create `MCPServer` objects in that namespace control)
and an origin pin that only the Secret's owner controls, so a team that can
edit its own `MCPServer` still cannot send the credential to a new host.
A team that can create an `MCPServer` with the same namespace and name as
an existing credentialed server already owns that namespace. Keeping the
values in a kubelet-mounted Secret means a compromised Gateway can read
only the credentials it already uses, never other Secrets. Values are read
per request from the mount and never cached in logs or memory beyond the
request. The pin makes plain `http` explicit: the administrator chooses it.

## Compatibility

Additive CRD change (`spec.credential`), in both CRD copies. New value
`global.upstreamCredentials.secretName`; when empty, no Secret is mounted
and servers declaring `credential` answer `503`. New traffic outcome
`credential_unavailable`. New `ToolsDiscovered` reason. Upgrades from
`0.0.5-beta.1` need the new CRD (SPEC-0009).

## Documentation

Operator and Gateway component docs (credentials), chart README (Secret
layout, rotation), `examples/kavryctl/` (credentialed server), README
security posture, CLAUDE.md "What works today".

## Decisions

- One Secret in the Kavrynt namespace, managed by the platform team, over
  per-namespace Secrets: it is the only design that needs no Secret RBAC.
- The origin pin is mandatory, not optional.
