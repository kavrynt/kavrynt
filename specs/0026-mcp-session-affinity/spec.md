# SPEC-0026: MCP session affinity

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-27 |
| Issue | #81 |
| ADRs | none |
| Supersedes | none |

## Summary

An MCP server that keeps per-session state can run more than one replica
behind the Gateway: every request of an MCP session (one `Mcp-Session-Id`)
reaches the pod that started it, with any number of Gateway replicas. When
a session is lost anyway, the Gateway says so in its metrics and console
instead of counting it as a client error.

## Problem

MCP Streamable HTTP lets a server assign a session in its `initialize`
answer (`Mcp-Session-Id`). Servers built with the official SDKs keep that
session in memory. The Gateway forwards to the server's Service, so with
three replicas requests of one session land on different pods: in testing
of `1.0.0-beta.2`, 8 of 10 agent sessions failed with `Session not found`.
The server answers those requests `404`, which the Gateway records as
`client_error`, so the upstream-errors alert never fires and nothing points
at the cause (#81).

`sessionAffinity: ClientIP` on the server's Service does not help: every
request comes from a Gateway pod.

## Goals

- Stateful MCP servers can scale out behind the Gateway.
- Lost sessions are visible and named, with or without affinity.
- No shared state between Gateway replicas.

## Non-goals

- Moving a session to another pod when its pod goes away. The session is
  lost, and the client starts a new one, as the MCP specification
  prescribes for a `404`.
- Affinity for endpoints outside the cluster, or for Services without
  endpoints the Gateway can see (ExternalName, headless Services without
  selectors).
- Load balancing beyond spreading new sessions across ready pods.

## Requirements

### Opting in

- **SPEC-0026-R01** `MCPServer.spec.sessionAffinity` MUST accept `None`
  (the default, today's behavior) and `MCPSession`. Other values MUST be
  rejected at admission.
- **SPEC-0026-R02** With `MCPSession`, `spec.endpoint` MUST name a
  Kubernetes Service by its cluster DNS name
  (`<service>.<namespace>.svc[.<cluster-domain>]`); other endpoints MUST be
  rejected by validation (`Accepted=False`, reason `InvalidSpec`, naming the
  field).

### Routing

- **SPEC-0026-R03** With `MCPSession`, a request without `Mcp-Session-Id`
  MUST be forwarded to one ready endpoint of the Service, chosen so that new
  sessions spread across ready pods.
- **SPEC-0026-R04** When that answer carries `Mcp-Session-Id`, every later
  request with the session ID the client received (`POST`, the `GET` event
  stream, and `DELETE`) MUST be forwarded to the same pod, whichever Gateway
  replica receives it.
- **SPEC-0026-R05** The Gateway replicas MUST NOT need shared state, a
  shared key, or each other to route a session (R04).
- **SPEC-0026-R06** The session ID the client receives MUST NOT reveal the
  pod's address or name. The MCP server MUST receive exactly the session ID
  it issued.
- **SPEC-0026-R07** A request whose session ID names a pod that is not a
  ready endpoint of the Service, or that the Gateway cannot read, MUST be
  answered `404` without being forwarded to another pod, so the client
  starts a new session.
- **SPEC-0026-R08** Requests to pod addresses MUST pass the same egress
  checks as Service endpoints (`internal/egress`).

### Discovery

- **SPEC-0026-R09** With `MCPSession`, the Operator's tool discovery MUST
  send every request of its MCP session to one pod.

### Visibility

- **SPEC-0026-R10** An upstream `404` to a request that carries
  `Mcp-Session-Id` MUST be recorded with outcome `session_not_found`, for
  every server, with or without affinity.
- **SPEC-0026-R11** The console MUST show, for a server with `None` whose
  calls end in `session_not_found`, that its sessions are being lost and
  that `spec.sessionAffinity: MCPSession` or a single replica fixes it.

### Access

- **SPEC-0026-R12** The Gateway MUST gain read-only access to EndpointSlices
  in the namespaces it watches, and nothing else: it keeps no access to
  Secrets and read-only access to `MCPServer`.

## Acceptance criteria

- Given a stateful MCP server with three replicas and `MCPSession`, and two
  Gateway replicas, when ten agents each run a session of twenty calls,
  then every call succeeds (R03, R04, R05).
- Given the same server with `None`, then calls fail with
  `session_not_found`, and the console names the cause (R10, R11).
- Given a session whose pod is deleted, when the client sends its next
  request, then it gets `404` and a new `initialize` succeeds on another
  pod (R07).
- Given `MCPSession` and an endpoint `https://mcp.example.com/mcp`, then the
  server is not Accepted and the message names `spec.endpoint` (R02).
- Given a session ID edited by the client, then the request gets `404` and
  no pod receives it (R07).
- Given any session, then the client's session ID contains neither the pod
  IP nor the pod name, and the server sees its own session ID (R06).

## Security and privacy

Session IDs from clients are untrusted. A client that edits the routing part
can at most reach another ready pod of the same Service, which does not know
the session and answers `404`, or get `404` from the Gateway (R07). Pod
addresses never reach clients (R06). The new EndpointSlice access is
read-only and limited to watched namespaces (R12). Pod addresses are checked
by the egress guard like any endpoint (R08), and the chart NetworkPolicies
(SPEC-0007) still apply, since they select pods.

## Compatibility

A CRD change, in both copies of the schema: the new optional field
`spec.sessionAffinity`. The default `None` keeps today's routing. The chart
gains an RBAC rule for EndpointSlices. The new outcome `session_not_found`
(R10) also applies to servers without affinity: requests that were
`client_error` before can now be `session_not_found`, and Prometheus queries
on `outcome="client_error"` change accordingly.

## Documentation

Gateway component docs (routing, outcomes), `MCPServer` reference, chart
README (RBAC), the console's outcome definitions, and a note in the
testing runbook. Until this is `Implemented`, the docs should say that a
server keeping session state needs one replica.

## Design notes (non-normative)

One way to meet R04–R07 without shared state: the Gateway returns
`<pod-tag>.<server-session-id>` to the client, where `<pod-tag>` is a short
hash of the pod's UID. Any replica resolves the tag against the Service's
current EndpointSlices, strips it, and forwards the rest. No key is needed:
a forged tag can only select another ready pod of the same Service, which
answers `404`. `plan.md` decides once this spec is accepted.

## Open questions

- Should `MCPSession` become the default for servers whose endpoint is a
  Service, once it has shipped? Recommended: not in this spec. Revisit after
  a release with the opt-in. The owner decides.
- Should the Gateway also pin sessions for servers that never return
  `Mcp-Session-Id` (stateless)? Recommended: no, since R04 applies only when
  a session exists.
