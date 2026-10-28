# SPEC-0031: Gateway audience

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-28 |
| Issue | #84 |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | SPEC-0006-R09, only for the Gateway audience and only when this is enabled |

## Summary

An opt-in Gateway audience lets one access token reach several MCP routes
of the same Gateway: the Gateway also accepts a token whose `aud` is its
`resourceURL`, for the routes the platform team lists. Agents that use many
servers then need one audience instead of one per route, and policy and
audit can tell which audience a call used.

## Problem

MCP client authentication (SPEC-0006) accepts a token only when its `aud`
contains the route's canonical URI, and refuses the Gateway's base URL
(SPEC-0006-R09). An agent that calls ten servers needs a token with ten
audiences. With Keycloak that is one audience mapper per route per client,
and every new route means changing every agent's client (#84,
`docs/AUTH-SETUP.md`, "Many servers per agent"). Authorization servers that
issue one audience per token cannot serve such an agent at all.

## Goals

- One token can reach the routes the platform team groups behind the
  Gateway audience.
- Per-route audiences keep working, and stay the default.
- Policy, audit, and the console can tell a Gateway-audience call from a
  route-audience call.

## Non-goals

- Changing Protected Resource Metadata: clients still discover each route
  as its own resource (SPEC-0006-R07).
- Mapping scopes to routes. A policy can do that with the input of R04.
- Token exchange changes: exchanged tokens keep the server's audience
  (SPEC-0015).

## Requirements

### Accepting the Gateway audience

- **SPEC-0031-R01** The chart value `gateway.auth.gatewayAudience.enabled`
  MUST default to `false`, which keeps SPEC-0006-R09 unchanged. The chart
  MUST refuse to render with it `true` while `gateway.auth.enabled` is
  `false`, since routes then take no tokens at all (SPEC-0006-R01).
- **SPEC-0031-R02** When enabled, a token whose `aud` contains
  `gateway.auth.resourceURL` exactly, and that passes every other check of
  SPEC-0006-R08, MUST be accepted for a route listed in
  `gateway.auth.gatewayAudience.routes`, and refused (SPEC-0006-R05) for
  every other route. The list holds route names and `<namespace>.*`
  patterns; the chart MUST refuse an empty list and the entry `*` when
  enabled. A list that names every route, one by one or by namespace, is
  the operator's explicit choice.
- **SPEC-0031-R03** A token whose `aud` contains the route's own URI MUST be
  accepted as today, whether or not the Gateway audience is enabled.

### Telling them apart

- **SPEC-0031-R04** The policy input MUST include `audience`: `route` when
  the token was accepted for the route's own URI, `gateway` when only for
  the Gateway audience.
- **SPEC-0031-R05** Audit events MUST record the same value.
- **SPEC-0031-R06** The Gateway MUST log at start, and the console MUST
  show, that the Gateway audience is enabled and for which routes.

## Acceptance criteria

- Given the default values, when a token with `aud: https://mcp.example.com`
  calls a route, then it gets `401` (R01).
- Given `gatewayAudience.enabled: true` and `routes: [team-a.*]`, when that
  token calls `team-a.search`, then it is accepted with policy input
  `audience: gateway`; when it calls `team-b.billing`, then it gets `401`
  (R02, R04).
- Given the same values, when a token for
  `https://mcp.example.com/mcp/team-b.billing` calls that route, then it is
  accepted with `audience: route` (R03).
- Given `routes: ["*"]`, or `routes: []`, with the Gateway audience
  enabled, then the chart refuses to render (R02).
- Given `gatewayAudience.enabled: true` and `gateway.auth.enabled: false`,
  then the chart refuses to render (R01).
- Given a call accepted for the Gateway audience, then its audit event
  records `audience: gateway`, and one accepted for the route's own URI
  records `audience: route` (R05).
- Given the Gateway audience is enabled for `team-a.*`, then the Gateway
  logs at start that it is enabled and for which routes, and the console
  shows the same (R06).

## Security and privacy

This weakens the audience binding that RFC 8707 and the MCP authorization
specification rely on: a token for the Gateway audience reaches every
listed route, so a server behind one of them that receives the caller's
token could replay it at the others. The Gateway never forwards caller
tokens (SPEC-0006-R13), so a server cannot get one through it, but a
token leaked elsewhere reaches more routes: every route the list matches.
The chart refuses only the obvious "everything" (`*`); a list that names
every route or every namespace reaches all of them, and the operator who
writes it accepts that a leaked token reaches every route. The value is
therefore off by default, needs an explicit route list, is visible in
policy and audit, and is warned about at start and in the console, naming
the routes. Policies that want per-route consent can deny
`audience: gateway` for sensitive routes.

## Compatibility

New Helm values in the schema, off by default; installations that do not
set them see no change. Policies that ignore `audience` are unaffected.
Audit events gain a field, which `kavryctl audit verify` must accept.

## Documentation

`docs/AUTH-SETUP.md` ("Many servers per agent"), the Gateway component docs
(MCP client authentication, policy input, audit fields), the chart README
and values, and `SECURITY.md`.

## Open questions

- Should the list also accept label selectors on `MCPServer`s instead of
  names? Recommended: names and namespace patterns first; selectors would
  let namespace owners add their servers to the list.
- Should the Gateway audience be an identifier other than `resourceURL`,
  for example `<resourceURL>/mcp`? Recommended: `resourceURL`, which is
  already the Gateway's identifier in its metadata. The owner decides.
