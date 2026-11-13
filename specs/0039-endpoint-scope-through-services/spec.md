# SPEC-0039: Endpoint scope through Services in the server's namespace

| Field | Value |
| --- | --- |
| Status | Implemented |
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
team's workloads through a Service of their own. It also makes the Gateway
and the Operator's tool discovery refuse, when they connect, a host name
from outside the cluster that resolves to a cluster address, and resolve
Service names only in the cluster's own DNS zone.

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

The review of `develop` at `da1b95f` (2026-11-12) confirmed the case those
docs describe, with `global.endpoints.clusterCIDRs` set to the cluster's
ranges: an endpoint `http://10-96-0-10.nip.io:8080/mcp`, or any DNS name
the namespace owner controls, passes the Operator's check, because only
IP literals are compared with the ranges (SPEC-0029-R06), and the
dial-time egress policy allows every cluster address. The chart renders no
egress NetworkPolicy for the Gateway, so nothing else stops it. An
`ExternalName` Service whose `externalName` is such a name is a third way
in, which R01 alone would not close.

Two more findings came up while this was built, and the requirements below
cover them:

- An EndpointSlice is written by anyone who may write EndpointSlices in
  the Service's namespace, for a Service with a selector as well, and its
  `targetRef` is whatever its author says. A check of the `targetRef`
  alone, or of Services without a selector alone, is passed by a slice
  that names another namespace's pod address and claims a pod of its own.
- A short Service name can also be a public DNS name. In a namespace named
  like a top-level domain (`dev`, `app`, `io`), `<name>.dev` is
  `<service>.<namespace>` to the Operator, and public DNS answers it
  whenever no Service of that name exists; `<service>.<namespace>.svc.<a
  zone someone owns>` reads as a Service name the same way. Comparing
  addresses is not enough for names that are trusted as Service names:
  they have to be resolved where only the cluster answers.

## Goals

- An `MCPServer` whose endpoint Service forwards to another namespace is
  not `Ready` (`EndpointNotAllowed`), unless
  `global.endpoints.crossNamespace` allows the pair.
- The check follows changes to the Service and its EndpointSlices.
- With the cluster's ranges configured, a host name from outside the
  cluster never connects the Gateway or the Operator to a cluster address,
  whatever it resolves to at that moment.

## Non-goals

- Attributing a cluster address reached through an outside name to a
  namespace. It cannot be done from the address alone, so such connections
  are refused for every namespace, and `global.endpoints.crossNamespace`
  does not apply to them.
- Clusters without `global.endpoints.clusterCIDRs`: R06 to R08 do nothing
  there, as SPEC-0029-R06 does nothing. An egress NetworkPolicy for the
  Gateway remains the control for those.
- Checking at dial time where a Service name of the server's own namespace
  leads. The Gateway has no Service access and keeps none (R05); R01, R02,
  and R08 cover those names in the Operator.
- Proving which pod an address in a hand-written EndpointSlice belongs to.
  That needs read access to Pods, whose specs hold environment values, so
  such addresses are refused instead (R02).
- The moment in which Kubernetes undoes a forged or edited EndpointSlice
  that carries its management label on a Service with a selector. Someone
  who may write EndpointSlices can repeat it, and the Operator accepts the
  slice while it lasts. Closing this needs the Pod read access above.
  Kubernetes grants namespace `edit` and `admin` roles no write access to
  EndpointSlices since 1.22, so this concerns clusters that grant it
  themselves; keep it away from namespace owners.
- An egress NetworkPolicy for the Gateway, and refusing a production
  install without `clusterCIDRs`: each needs new chart values or a new
  default and gets its own spec.

## Requirements

<!-- One observable behavior per requirement. RFC 2119 keywords. Never renumber. -->

- **SPEC-0039-R01** When the endpoint host names a Service of type
  `ExternalName` in the `MCPServer`'s namespace, or in one
  `global.endpoints.crossNamespace` allows for it, the Operator MUST apply
  SPEC-0029-R04 to R06 to the Service's `externalName` as if it were the
  endpoint host, following at most 5 `ExternalName` Services, and MUST set
  `Ready` to `False` with reason `EndpointNotAllowed` when any of them
  names another namespace, or when the chain is longer. R02 MUST apply to
  the Service the chain ends at.
- **SPEC-0039-R02** When the endpoint host names a Service, with or
  without a selector, the Operator MUST set `Ready` to `False` with reason
  `EndpointNotAllowed` when one of the EndpointSlices labelled for the
  Service has an endpoint whose `targetRef` is in a namespace that
  `global.endpoints.crossNamespace` does not allow for the `MCPServer`'s
  namespace, or an endpoint with an address inside
  `global.endpoints.clusterCIDRs` in a slice that Kubernetes does not
  manage for a Service with a selector (the slice lacks the label
  `endpointslice.kubernetes.io/managed-by: endpointslice-controller.k8s.io`,
  or the Service has no selector). The namespace of such an address cannot
  be identified, whatever its `targetRef` says, so no namespace pair
  allows it.
- **SPEC-0039-R03** The Operator MUST check R01 and R02 again when the
  Service or its EndpointSlices change, and within 5 minutes otherwise.
- **SPEC-0039-R04** A Service the Operator cannot read MUST keep the
  server not `Ready` (`EndpointNotAllowed`), checked again after 30 s, as
  SPEC-0029 does for a namespace it cannot read.
- **SPEC-0039-R05** The Operator's RBAC MUST grow only by `get`, `list`,
  and `watch` on Services and `list` and `watch` on EndpointSlices; the
  Gateway's RBAC MUST NOT change.


### Outside names that resolve to cluster addresses

- **SPEC-0039-R06** When `global.endpoints.clusterCIDRs` is configured and
  a route's endpoint host is neither a Service name (the forms of
  SPEC-0029-R04, and names without a dot) nor an IP literal, the Gateway
  MUST refuse to connect to an address inside those ranges, at the moment
  it connects and for every connection, MUST answer `502` as it does for
  an address its egress policy refuses, and MUST NOT send the request, the
  upstream credential, or an exchanged token.
- **SPEC-0039-R07** The Operator's tool discovery MUST refuse the same
  connections as R06. The failed discovery MUST NOT change `Ready`
  (SPEC-0027), and its message MUST NOT include the resolved address.
- **SPEC-0039-R08** When `global.endpoints.clusterCIDRs` is configured and
  an `ExternalName` chain (R01) ends at a name that is not a Service name,
  the Operator MUST set `Ready` to `False` with reason
  `EndpointNotAllowed`: the Gateway would treat the endpoint as a Service
  of the server's namespace and could not apply R06.
- **SPEC-0039-R09** The Gateway MUST take the ranges of R06 from
  `global.endpoints.clusterCIDRs`, the value the Operator already uses;
  the chart MUST NOT gain a value for them, and a malformed range MUST
  stop the Gateway at start, as it stops the Operator.
- **SPEC-0039-R10** When `global.endpoints.clusterCIDRs` is configured,
  the Gateway and the Operator's tool discovery MUST resolve an endpoint
  host that is a Service name with a dot (R06) as its full name in the
  cluster's DNS zone (`<...>.svc.<cluster domain>.`, the domain taken from
  the search path of the component's resolver configuration, or
  `cluster.local`), so that the same name in another zone is never asked.
  A name with `svc` or `pod` followed by anything but the cluster domain
  is not a Service name. A `<service>.<namespace>` or
  `<pod>.<service>.<namespace>` name is a Service name when the namespace
  is the `MCPServer`'s own or one `global.endpoints.crossNamespace` allows
  for it; when it does not exist in the cluster's zone, it MUST be
  connected as a name from outside the cluster (R06).

## Acceptance criteria

- Given an `ExternalName` Service in `team-a` pointing at a Service in
  `team-b`, when an `MCPServer` in `team-a` uses it, then the server is not
  `Ready` with `EndpointNotAllowed`, and `/mcp/team-a.<name>` answers
  `404` (SPEC-0039-R01).
- Given a selector-less Service in `team-a` whose EndpointSlice names a
  `team-b` pod, when an `MCPServer` uses it, then the server is not `Ready`
  with `EndpointNotAllowed` (SPEC-0039-R02).
- Given `clusterCIDRs`, and a Service in `team-a` with an EndpointSlice
  written by hand that lists `team-b`'s pod address and claims a `team-a`
  pod as its `targetRef`, then the server is not `Ready`, whether or not
  the Service has a selector (SPEC-0039-R02).
- Given `clusterCIDRs`, a namespace `dev`, and an `MCPServer` in it whose
  endpoint host `<name>.dev` is no Service there and is answered by public
  DNS with `team-b`'s Service address, then calls get `502`; answered with
  a public address, the server is reached (SPEC-0039-R10).
- Given `clusterCIDRs` and an endpoint `<service>.<namespace>` in the
  server's own namespace, then the Service is reached as before
  (SPEC-0039-R10).
- Given an `ExternalName` Service in `team-a` pointing at that selector-less
  Service, when an `MCPServer` uses the `ExternalName` Service, then the
  server is not `Ready` with `EndpointNotAllowed` (SPEC-0039-R01, R02).
- Given a `Ready` server behind a Service in its own namespace, when the
  Service is changed to `ExternalName` pointing at `team-b`, then within
  5 minutes the server is not `Ready` (SPEC-0039-R03).
- Given `global.endpoints.crossNamespace` allows `team-a` to `team-b`, then
  both cases above stay `Ready` (SPEC-0039-R01, R02).
- Given `clusterCIDRs` covers the Service range and an `MCPServer` in
  `team-a` whose endpoint host is an outside DNS name that resolves to
  `team-b`'s Service address, when a caller uses `/mcp/team-a.<name>`,
  then the answer is `502`, `team-b`'s server receives nothing, and the
  server's tools are not discovered (SPEC-0039-R06, R07).
- Given the same name first resolves to a public address and later to a
  cluster address, then calls after the change get `502` (SPEC-0039-R06).
- Given an `ExternalName` Service in `team-a` whose `externalName` is an
  outside DNS name, and `clusterCIDRs` configured, when an `MCPServer` uses
  it, then the server is not `Ready` with `EndpointNotAllowed`
  (SPEC-0039-R08).
- Given an `MCPServer` whose endpoint is an outside name resolving to a
  public address, then it works as before (SPEC-0039-R06).
- Given no `clusterCIDRs`, then outside names connect as before
  (SPEC-0039-R06).

## Security and privacy

Service specs and EndpointSlices are read, never written, and never shown
in status messages beyond the Service name, which the `MCPServer`'s author
already wrote. The check fails closed: a Service that cannot be read, or an
`ExternalName` chain that is too long, keeps the server not `Ready`.
Without configured cluster ranges, an EndpointSlice address cannot be told
from an outside one and stays allowed unless its `targetRef` names another
namespace, as an IP literal does today (SPEC-0029-R06): only clusters with
`global.endpoints.clusterCIDRs` are protected against a slice whose
`targetRef` is false. The Operator caches every Service and EndpointSlice
of the cluster to follow their changes.

## Compatibility

No CRD change. A server that uses such a Service today, legitimately,
becomes not `Ready` on upgrade until the platform team lists the pair in
`global.endpoints.crossNamespace`; the release notes must say so. With
`clusterCIDRs`, a Service without a selector whose hand-written
EndpointSlices (or mirrored `Endpoints`) list pod addresses is refused
even in its own namespace: give the Service a selector. The Operator's
ClusterRole gains read access to Services and `watch` on EndpointSlices.

With `clusterCIDRs` configured, a server registered under an outside name
that resolves to a cluster address (for example a company DNS name for an
in-cluster ingress) stops working on upgrade with `502`, and one behind an
`ExternalName` Service to an outside name becomes not `Ready`. Both must
be registered under the Service name of their namespace, or the outside
name directly; the release notes must say so. Installs without
`clusterCIDRs` see no change. The Gateway gains the flags `--cluster-cidrs`
and `--endpoint-cross-namespace`, set by the chart from the existing
values. An endpoint of the form `<service>.<namespace>.svc.<domain>` whose
domain is not the cluster's (a cluster with a domain the resolver
configuration does not name) is connected as an outside name and gets
`502` with `clusterCIDRs`.

## Documentation

Operator component docs (endpoint scope table), the chart README (RBAC),
the `KavryntMCPServerNotReady` runbook, and `ROADMAP.md`.

## Decisions

The owner approved the spec on 2026-11-13 with these answers to its open
questions:

- EndpointSlice addresses without configured cluster ranges stay allowed
  (R02): refusing them would refuse Services in front of databases and
  machines outside the cluster.
- The Gateway attributes no address to a namespace at dial time; it
  refuses outside names only (R06).
- An egress NetworkPolicy for the Gateway, and a production profile that
  requires `clusterCIDRs`, are left to their own specs (non-goals).
