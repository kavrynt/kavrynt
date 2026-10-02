# ADR-0003: Runtime Authorization In The Gateway

## Status

Accepted (2026-10-02). Design only; nothing in this ADR is implemented in
`0.0.1-beta.1`.

Plane 4 of `kavrynt-cloud/docs/ADR-0006-Identity-Planes.md`. Threats are
tracked in `kavrynt-cloud/docs/THREAT-0001-System-Threat-Model.md`.

## Context

The gateway in `0.0.1-beta.1` routes `POST /mcp/<namespace>.<name>` to an
upstream endpoint. It does not authenticate callers, evaluate policy, or audit
calls, and it copies request headers, including `Authorization`, to the
upstream server. That last behaviour is token passthrough, which the MCP
authorization specification forbids for compliant servers.

Standards baseline: MCP specification revision `2026-07-28`. That revision
removes protocol sessions (each request is self-contained), adds mandatory
`Mcp-Method` and `Mcp-Name` headers on Streamable HTTP requests, deprecates
the legacy HTTP+SSE transport, prefers Client ID Metadata Documents over
Dynamic Client Registration, requires clients to validate `iss` (RFC 9207),
and recognises the Enterprise-Managed Authorization extension (ID-JAG).

## Decision

### The gateway is an OAuth 2.1 resource server

- Every request to `/mcp/...` requires `Authorization: Bearer`. Tokens in query
  strings are rejected.
- Trusted authorization servers are configured per installation (Helm values),
  pointing at the **customer's** IdP or authorization server. Kavrynt Cloud is
  not in this path.
- The gateway serves Protected Resource Metadata (RFC 9728) for each route and
  returns `401` with `WWW-Authenticate: Bearer resource_metadata=...` and the
  required `scope` when no valid token is presented, and `403` with
  `error="insufficient_scope"` when scopes are missing.
- Validation: allow-listed issuer, signature against cached JWKS, `exp`/`nbf`,
  allow-listed asymmetric algorithms, and audience equal to the route's
  canonical URI (RFC 8707). Tokens for any other audience are rejected.
- Enterprise customers whose IdP issues ID-JAG use the MCP
  Enterprise-Managed Authorization extension; the gateway's resource-server
  behaviour is the same.

### No token passthrough

- The gateway strips `Authorization`, cookies, and any configured sensitive
  headers before forwarding.
- For upstream servers that require authorization, the gateway obtains a
  token for that server's audience through OAuth 2.0 Token Exchange
  (RFC 8693), with the caller's token as `subject_token`. The resulting token
  preserves the user (`sub`) and the acting agent (`act` claim) where the
  authorization server supports it.
- Static upstream credentials, when unavoidable, come from Kubernetes Secrets
  referenced by the `MCPServer` resource and are never logged.

### Policy decision point inside the gateway

- Engine: Open Policy Agent embedded as a Go library.
- Input: verified identity (subject, groups, client, actor), route, MCP method
  and name (from the `Mcp-Method` / `Mcp-Name` headers, cross-checked against
  the body), tool risk metadata, and request context.
- Output: `allow`, `deny`, or `require_approval`, with a reason recorded in
  audit.
- Default deny. A route with no applicable policy is denied.
- Policies are authored and versioned in Kavrynt Cloud, delivered as bundles
  signed with a Kavrynt key, and verified before load. The gateway keeps the
  last known-good bundle and continues to enforce it if Kavrynt Cloud is
  unreachable. Without any valid bundle the gateway fails closed.
- Self-hosted installations without Kavrynt Cloud load bundles from a
  ConfigMap or OCI artifact, with the same signature check.

### Approvals

High-risk tool calls (`require_approval`) are held, and the client receives a
pending result. The decision is made in Kavrynt Cloud by an authorised
approver. OpenID CIBA (Client-Initiated Backchannel Authentication) is
evaluated as the standard for this flow before a custom protocol is designed.

### Audit

- One audit event per decision: who (user, client, actor), which tool, the
  decision and reason, policy bundle version, latency, and outcome.
  Arguments and results are not recorded by default; recording is an
  explicit per-tool setting because they may contain customer data.
- Events are buffered locally, hash-chained per gateway instance, and sent
  outbound to Kavrynt Cloud in batches with the cluster's plane-3 credentials.

### Gateway bypass

The chart ships an optional NetworkPolicy so labelled MCP server pods accept
traffic only from the gateway. Documentation states that governance is only as
strong as this enforcement.

## Consequences

- `0.0.2-beta.1` removes the registry (ADR-0002); authentication and policy
  ship in a later beta. Customer documentation keeps stating that the gateway
  is unauthenticated until then.
- Header stripping (no passthrough) is a small, separable fix and should land
  before authentication.
- The gateway gains dependencies on a JWT/JWKS library and OPA; both must pass
  govulncheck and Trivy gates.
- Version `1.0.0-beta.1` is reserved for when authentication, policy, and audit
  are implemented and tested end to end.
