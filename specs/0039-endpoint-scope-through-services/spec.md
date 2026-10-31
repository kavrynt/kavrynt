# SPEC-0039: Endpoint scope through Services in the server's namespace

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-31 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

Endpoint scope (SPEC-0029) attributes an `MCPServer` endpoint to a
namespace by its host name. A Service in the server's own namespace can
still send traffic to another namespace: an `ExternalName` Service whose
name points there, or a Service without a selector whose EndpointSlices
list another namespace's pods. This spec makes the Operator follow such
Services, so a namespace owner cannot route the Gateway to another
team's workloads through a Service of their own.

## Problem

The pentest of `develop` at `7328a95` (2026-10-31) found two ways past
SPEC-0029 for someone who may create Services and `MCPServer`s in one
namespace (`team-a`):

- An `ExternalName` Service `alias` in `team-a` with
  `externalName: other.team-b.svc.cluster.local`, and an `MCPServer` whose
  endpoint is `http://alias.team-a.svc.cluster.local:8080`. The server is
  `Ready`, its tools are discovered, and calls through `/mcp/team-a.alias`
  reach `team-b`'s MCP server.
- A Service `manual` in `team-a` without a selector, with an EndpointSlice
  naming `team-b`'s pod address, and an `MCPServer` for it. Calls through
  `/mcp/team-a.manual` reach `team-b`'s MCP server.

Either way, `team-b`'s server is reached on a route whose policy and audit
belong to `team-a`, and the chart's NetworkPolicies (SPEC-0007) let the
traffic in, because it comes from the Gateway. The Operator docs already
say that external names resolving to cluster addresses are not detected;
these two cases stay inside the cluster's own names and are not mentioned.

## Goals

- An `MCPServer` whose endpoint Service forwards to another namespace is
  not `Ready` (`EndpointNotAllowed`), unless
  `global.endpoints.crossNamespace` allows the pair.
- The check follows changes to the Service and its EndpointSlices.

## Non-goals

- External names that resolve to cluster addresses (SPEC-0029 non-goal);
  an egress NetworkPolicy for the Gateway remains the control for those.
- Checking where the Gateway connects at dial time; the Gateway keeps its
  egress policy (`internal/egress`) for addresses.

## Requirements

<!-- One observable behavior per requirement. RFC 2119 keywords. Never renumber. -->

- **SPEC-0039-R01** When the endpoint host names a Service of type
  `ExternalName` in the `MCPServer`'s namespace, the Operator MUST apply
  SPEC-0029-R04 to R06 to the Service's `externalName` as if it were the
  endpoint host, following at most 5 `ExternalName` Services, and MUST set
  `Ready` to `False` with reason `EndpointNotAllowed` when any of them
  names another namespace, or when the chain is longer.
- **SPEC-0039-R02** When the endpoint host names a Service without a
  selector in the `MCPServer`'s namespace, the Operator MUST set `Ready` to
  `False` with reason `EndpointNotAllowed` when one of the Service's
  EndpointSlices has an endpoint whose `targetRef` is in another
  namespace, or that has no `targetRef` and an address inside
  `global.endpoints.clusterCIDRs`.
- **SPEC-0039-R03** The Operator MUST check R01 and R02 again when the
  Service or its EndpointSlices change, and within 5 minutes otherwise.
- **SPEC-0039-R04** A Service the Operator cannot read MUST keep the
  server not `Ready` (`EndpointNotAllowed`), checked again after 30 s, as
  SPEC-0029 does for a namespace it cannot read.
- **SPEC-0039-R05** The Operator's RBAC MUST grow only by `get`, `list`,
  and `watch` on Services and `watch` on EndpointSlices; the Gateway's RBAC
  MUST NOT change.

## Acceptance criteria

- Given an `ExternalName` Service in `team-a` pointing at a Service in
  `team-b`, when an `MCPServer` in `team-a` uses it, then the server is not
  `Ready` with `EndpointNotAllowed`, and `/mcp/team-a.<name>` answers
  `404` (SPEC-0039-R01).
- Given a selector-less Service in `team-a` whose EndpointSlice names a
  `team-b` pod, when an `MCPServer` uses it, then the server is not `Ready`
  with `EndpointNotAllowed` (SPEC-0039-R02).
- Given a `Ready` server behind a Service in its own namespace, when the
  Service is changed to `ExternalName` pointing at `team-b`, then within
  5 minutes the server is not `Ready` (SPEC-0039-R03).
- Given `global.endpoints.crossNamespace` allows `team-a` to `team-b`, then
  both cases above stay `Ready` (SPEC-0039-R01, R02).

## Security and privacy

Service specs and EndpointSlices are read, never written, and never shown
in status messages beyond the Service name, which the `MCPServer`'s author
already wrote. The check fails closed: a Service that cannot be read, or an
`ExternalName` chain that is too long, keeps the server not `Ready`. A
selector-less Service whose endpoints have no `targetRef` and no
configured cluster ranges cannot be attributed and stays allowed, as an IP
literal does today (SPEC-0029-R06).

## Compatibility

No CRD change. A server that uses such a Service today, legitimately,
becomes not `Ready` on upgrade until the platform team lists the pair in
`global.endpoints.crossNamespace`; the release notes must say so. The
Operator's ClusterRole gains read access to Services.

## Documentation

Operator component docs (endpoint scope table), the chart README (RBAC),
the `KavryntMCPServerNotReady` runbook, and `ROADMAP.md`.

## Open questions

- Should R02 refuse selector-less Services whose endpoints carry no
  `targetRef`, even without `clusterCIDRs`? Safer, but it refuses Services
  in front of databases or VMs outside the cluster. Owner decides.
- Should the Gateway also refuse, at dial time, addresses it can attribute
  to another namespace through the EndpointSlices it already watches?
  Owner decides.
