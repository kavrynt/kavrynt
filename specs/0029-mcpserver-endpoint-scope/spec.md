# SPEC-0029: MCPServer endpoint scope

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-28 |
| Issue | none (security review) |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

An `MCPServer` names an endpoint, and the Gateway acts on it with its own
network position and OAuth client. This spec limits what that lets an
`MCPServer` author reach: the Gateway forwards only what MCP Streamable HTTP
needs, endpoints stay in the server's own namespace unless the platform
team allows more, and token exchange asks only for audiences the platform
team allowed for that namespace.

## Problem

Anyone who may create an `MCPServer` in a watched namespace chooses its
endpoint and, with SPEC-0015, the audience the Gateway exchanges callers'
tokens for. Today:

- `/mcp/<ns>.<name>[/<suffix>]` forwards any HTTP method, path suffix, and
  query to the endpoint's host. MCP Streamable HTTP needs one URL with
  `POST`, `GET`, and `DELETE`.
- An endpoint may name a Service in any namespace. The chart's
  NetworkPolicies (SPEC-0007) admit the Gateway to labelled MCP server pods
  everywhere, so a route in one namespace can reach a server another team
  isolated behind the Gateway, under the first team's route and policy.
- `spec.tokenExchange.audience` may be any URI. The Gateway uses one OAuth
  client for every route, so the authorization server cannot tell which
  route asked for an audience.

## Goals

- A route forwards only MCP Streamable HTTP requests to its endpoint URL.
- Namespace owners cannot point a route at another namespace's Services
  unless the platform team allows it.
- Namespace owners cannot have callers' tokens exchanged for audiences the
  platform team did not allow for their namespace.

## Non-goals

- Detecting external host names that resolve to cluster addresses. The
  dial-time egress policy (`internal/egress`) stays the boundary for
  addresses; this spec covers names the Operator can attribute to a
  namespace.
- Per-route authorization of callers, which is the policy's job
  (SPEC-0012).

## Requirements

### Forwarded requests

- **SPEC-0029-R01** The Gateway MUST forward only `POST`, `GET`, and
  `DELETE` requests to an MCP route, and MUST answer other methods with
  `405` and an `Allow: GET, POST, DELETE` header, without contacting the
  server.
- **SPEC-0029-R02** By default the Gateway MUST forward only requests for
  the route itself (`/mcp/<ns>.<name>`), and MUST answer a request with a
  path suffix with `404` without contacting the server.
- **SPEC-0029-R03** An `MCPServer` MAY set `spec.forwardSubpaths: true`;
  the Gateway then forwards path suffixes as it does today, still refusing
  dot segments.

### Endpoint namespaces

- **SPEC-0029-R04** The Operator MUST set `Ready` to `False` with reason
  `EndpointNotAllowed` when the endpoint's host names a Kubernetes Service
  (`<service>.<namespace>`, `<service>.<namespace>.svc`, or
  `<service>.<namespace>.svc.<cluster domain>`) in another namespace than
  the `MCPServer`'s, so the Gateway does not route it.
- **SPEC-0029-R05** The chart value
  `global.endpoints.crossNamespace` MUST list namespace pairs
  (`from`, `to`) for which R04 does not apply; it is empty by default.
- **SPEC-0029-R06** The Operator MUST treat an IP-literal endpoint host
  inside the cluster's Service or Pod ranges as another namespace's
  (R04), when those ranges are configured in
  `global.endpoints.clusterCIDRs`.

### Token exchange audiences

- **SPEC-0029-R07** The chart value
  `global.tokenExchangeAudiences` MUST map namespaces to the
  audiences their `MCPServer`s may request; `"*"` as a namespace applies to
  all.
- **SPEC-0029-R08** The Operator MUST set `Ready` to `False` with reason
  `AudienceNotAllowed` for an `MCPServer` whose
  `spec.tokenExchange.audience` is not allowed for its namespace, and the
  Gateway MUST NOT exchange a token for an audience not allowed for the
  route's namespace, answering `503` with `exchange_failed`.

## Acceptance criteria

- Given a route, when a client sends `PUT /mcp/<ns>.<name>`, then the
  answer is `405` with `Allow`, and the server receives nothing (R01).
- Given a route without `forwardSubpaths`, when a client sends
  `GET /mcp/<ns>.<name>/other`, then the answer is `404` and the server
  receives nothing; with `forwardSubpaths: true` the request is forwarded
  (R02, R03).
- Given an `MCPServer` in `team-b` whose endpoint is
  `http://search.team-a.svc:8080/mcp`, then it is `Ready False` with
  `EndpointNotAllowed` and has no route; after `team-b` to `team-a` is
  allowed, it is routed (R04, R05).
- Given `global.tokenExchangeAudiences: {team-a: [https://tickets.example.com]}`,
  when an `MCPServer` in `team-b` asks for that audience, then it is
  `Ready False` with `AudienceNotAllowed`, and no exchange is attempted
  for its route (R07, R08).

## Security and privacy

The `MCPServer` spec is untrusted input from namespace owners. The checks
fail closed: an endpoint the Operator cannot attribute is routed only
under the existing egress policy, a disallowed audience is never sent to
the authorization server, and refused requests never reach the server.
Condition messages name the namespace and audience from the spec, never
addresses or tokens.

## Compatibility

A CRD change (`spec.forwardSubpaths`, two reasons) in both copies, and new
Helm values in the schema. R02, R04, and R08 change default behavior:
servers that rely on sub-paths, cross-namespace endpoints, or unlisted
audiences stop being routed after an upgrade until the values allow them.
Release notes must list each, and `kavryctl upgrade` checks should report
servers that would stop being routed.

## Documentation

Gateway and Operator component docs, the chart README and values,
`SECURITY.md`, the `KavryntMCPServerNotReady` runbook, and release notes.

## Open questions

- Should R02, R04, and R08 be on by default (secure, breaking) or opt-in
  for one release with a warning first? Recommended: on by default, since
  the runtime is in beta and the upgrade check names affected servers. The
  owner decides.
- Should cross-namespace exceptions live in the chart (R05) or in a
  cluster-scoped resource the platform team owns? Recommended: the chart
  for now.
