# SPEC-0026 plan: MCP session affinity

## Decisions

- `MCPSession` stays opt-in (open question 1, as recommended).
- Servers that never return `Mcp-Session-Id` are not pinned (open
  question 2, as recommended): a request without a session ID spreads.
- With `MCPSession` the Service must have one port: EndpointSlices list
  target ports, and mapping the endpoint's Service port to one would need
  read access to Services, which R12 rules out. Slices with several ports
  are not used, so new sessions get `503`; the docs say so.
- With `MCPSession` the endpoint must use `http`. The Gateway connects to a
  pod's address, so a TLS certificate issued for the Service name would not
  verify; TLS to pods is a later change (a mesh can add mTLS today). The
  validation message names `spec.endpoint` (R02).

## Design

- **API (R01, R02).** `MCPServerSpec.SessionAffinity` (`None` or
  `MCPSession`, empty means `None`); the CRD copies declare the enum and the
  default. `ServiceEndpoint(endpoint)` in `api/v1alpha1` parses
  `http://<service>.<namespace>.svc[.<cluster-domain>][:port]/...` into the
  Service's namespace, name, and port (80 when absent); `Validate` reports
  `spec.endpoint must name a Kubernetes Service (http://<service>.<namespace>.svc[.<cluster-domain>]) when spec.sessionAffinity is MCPSession`.
- **Package `internal/mcpsession`.** No Kubernetes clients, shared by the
  Gateway and the Operator:
  - `Ready(slices, allowLoopback)` returns the ready endpoints of a
    Service's single-port EndpointSlices: `ready` and `serving` not
    `false`, `terminating` not `true`, a pod `targetRef` with a UID, and an
    address the egress policy allows (`egress.Allowed`, R08), at the
    slice's port.
    Each has a `Tag`: the first 16 hex characters of SHA-256 of the pod UID,
    so it names neither the address nor the pod (R06). Sorted by tag.
  - `Wrap(tag, id)` returns `<tag>.<id>`; `Unwrap(id)` splits it and
    rejects anything but 16 lowercase hex characters, a dot, and a
    non-empty rest.
- **Gateway routing (R03 to R07).** `model.Route.SessionAffinity`. The
  informer cache also holds EndpointSlices of the watched namespaces, with
  managed fields dropped. `kube.Endpoints` lists a Service's slices from the
  cache by `kubernetes.io/service-name`. For an `MCPSession` route the
  handler:
  - without `Mcp-Session-Id`, takes the next ready endpoint (a counter per
    handler) or answers `503` (`gateway_error`) when there is none;
  - with one, unwraps it and takes the ready endpoint with that tag, or
    answers `404` (`session_not_found`) without forwarding (R07);
  - sends the request to the pod's address with the Service host in
    `Host` and the server's own session ID (R06), through the same
    egress-checked client (R08);
  - wraps an `Mcp-Session-Id` in the answer with the pod's tag.
  Replicas share nothing: the tag and the EndpointSlices are enough (R05).
- **Discovery (R09).** The Operator lists the Service's EndpointSlices with
  its uncached API reader at each discovery, takes the first ready
  endpoint, and `discovery.Client.ListTools` gets that address: every
  request of the session goes to it with the Service host in `Host`. No
  ready endpoint fails the discovery (`DiscoveryFailed`).
- **Outcome (R10).** `session_not_found`: an upstream `404` to a request
  with `Mcp-Session-Id`, for every server, and the Gateway's own `404` of
  R07. It is in the `other` class of the dashboards and not an upstream
  error for the alerts: clients see it also when a session expires.
- **Console (R11).** `model.Server.sessionAffinity`. A DOM-free
  `sessionLoss(server, requests)` in `stats.js` reports, for a server
  without `MCPSession`, how many of its recent requests ended in
  `session_not_found`; the server page then says its sessions are being
  lost and that `spec.sessionAffinity: MCPSession` or one replica fixes it.
- **Access (R12).** The Gateway's ClusterRole, or each watched namespace's
  Role, gains `get`, `list`, `watch` on `discovery.k8s.io` `endpointslices`.
  The Operator's ClusterRole gains `list` on them (R09), in the chart and
  `config/rbac`.

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `charts/kavrynt/tests/crd_test.yaml`, `v1alpha1.TestSessionAffinity` |
| R02 | `v1alpha1.TestSessionAffinityNeedsAServiceEndpoint` |
| R03, R04, R05 | `api.TestSessionAffinityPinsSessionsToPods` |
| R06 | `api.TestSessionAffinityHidesPods`, `mcpsession.TestTagsNameNoPod` |
| R07 | `api.TestSessionAffinityRefusesUnknownSessions` |
| R08 | `mcpsession.TestReadySkipsAddressesEgressRefuses` |
| R09 | `controller.TestDiscoveryUsesOnePodWithSessionAffinity`, `discovery.TestListToolsAtOneAddress` |
| R10 | `api.TestLostSessionsAreNamed` |
| R11 | `test/console/stats.test.mjs` |
| R12 | `charts/kavrynt/tests/gateway_test.yaml`, `scripts/e2e-kind.sh` |
| E2E | `scripts/e2e-kind.sh`: a three-replica stateful server with `MCPSession` keeps ten sessions through two Gateway replicas |

## Rollout and risks

- The Gateway's cache now holds the EndpointSlices of its namespaces. In a
  large cluster without `watchNamespaces` that costs memory; managed fields
  are dropped to keep it small.
- A Gateway without the new RBAC (a manifest applied by hand) cannot sync
  its cache and stays unready, which the upgrade check reports.
- `client_error` counts drop where `session_not_found` now applies; the
  release notes say so.
