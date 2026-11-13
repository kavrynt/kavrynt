# SPEC-0039 plan: Endpoint scope through Services in the server's namespace

## Decisions

- The Operator follows Services; the Gateway does not. The Gateway keeps
  its RBAC (R05) and decides from the host name and the address it is about
  to connect to, which need no API access.
- A `targetRef` is not evidence. Whoever writes an EndpointSlice writes its
  `targetRef`, so an address in the cluster's ranges is accepted only in a
  slice the EndpointSlice controller manages for a Service with a selector.
  Reading Pods to prove an address was rejected: Pod specs hold environment
  values, and the Operator has no reason to read them.
- Comparing addresses is not enough for names trusted as Service names. A
  short name, or `<service>.<namespace>.svc.<someone's zone>`, can be
  answered by public DNS. With the cluster's ranges configured, a Service
  name is therefore connected as its full name in the cluster's zone, with
  a trailing dot, so the resolver's search path and public DNS are out of
  the question.
- DNS answers an `ExternalName` Service with its target as a full name, so
  only `<service>.<namespace>.svc.<cluster domain>` leads to another
  Service; every other target is resolved outside the cluster and is
  refused with cluster ranges (R08).
- No new chart value (R09): the Gateway gets `global.endpoints.clusterCIDRs`
  and `crossNamespace` as flags, and both components read the cluster
  domain from the search path in `/etc/resolv.conf` (`svc.<domain>`),
  falling back to `cluster.local`, which nobody outside can register.

## Design

`internal/scope`: `Policy.EndpointServices` checks the endpoint host as
before, then follows the Service it names through `Policy.Services` (R01,
R02, R08) and returns the Services it read, found or not. `Policy.Endpoint`
is the same check without the list.

`internal/operator/controller`: `ServiceReader` reads a Service and the
EndpointSlices labelled for it from the manager's cache. The reconciler
records the Services of each server's last check and watches Services and
EndpointSlices; an event checks the servers recorded for that Service, and
every server behind a Service is checked again after 5 minutes (R03). A
read error keeps the server not `Ready` and retries after 30 s (R04).

`internal/egress`: `Cluster` holds the ranges, the cluster domain, and the
allowed namespace pairs. `Cluster.Context` classifies an endpoint host for
an `MCPServer`'s namespace and stores the result in the request context;
`ClusterTransport` reads it to pick its pool and when it connects. An outside name is connected
with a dialer whose `Control` also refuses the cluster's ranges (R06); a
Service name is connected as its full name (R10); a short name that does
not exist in the cluster's zone falls back to an outside name. A request
without the context, or to another host than it names, is an outside name.
The Gateway sets the context in `Handler.proxy`, the Operator before
`ListTools` (R07).

## Affected files

| Path | Change |
| --- | --- |
| `internal/egress/cluster.go` | `Cluster`, `ClusterTransport`, `ClusterDomainFrom` |
| `internal/egress/egresstest` | DNS without a network, for tests |
| `internal/scope/scope.go` | `EndpointServices`, Service and EndpointSlice rules |
| `internal/operator/controller/mcpserver_controller.go` | `ServiceReader`, watches, recheck, discovery context |
| `internal/operator/discovery/errors.go` | Message for a refused outside name |
| `internal/gateway/api/api.go` | `SetCluster`, context per proxied request |
| `cmd/gateway/main.go`, `cmd/operator/main.go` | Flags and wiring |
| `config/rbac/role.yaml`, `charts/kavrynt/charts/k8s-operator/templates/rbac.yaml` | Services, `watch` on EndpointSlices |
| `charts/kavrynt/charts/gateway/templates/deployment.yaml` | `--cluster-cidrs`, `--endpoint-cross-namespace` |
| `scripts/e2e-kind.sh` | Cluster ranges in the install; R01, R02, R03, R08, R10 |
| Docs | Operator and Gateway component docs, chart README, runbook, roadmap |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `scope.TestExternalNameServicesAreFollowed`, `scope.TestExternalNameChainsAreBounded`, `controller.TestServicesThatLeadElsewhereAreNotReady`, `scripts/e2e-kind.sh` |
| R02 | `scope.TestEndpointSlicesAreFollowed`, `controller.TestServicesThatLeadElsewhereAreNotReady`, `scripts/e2e-kind.sh` |
| R03 | `scope.TestEndpointServicesReportsWhatToWatch`, `controller.TestServiceChangesCheckTheServerAgain`, `scripts/e2e-kind.sh` |
| R04 | `scope.TestUnreadableServiceIsRefused`, `controller.TestUnreadableServiceIsRetried` |
| R05 | chart `operator_endpointslices_test.yaml`, `gateway_test.yaml` |
| R06 | `egress.TestOutsideNamesNeverReachClusterAddresses`, `egress.TestOutsideNameIsCheckedAtEveryConnection`, `egress.TestUnclassifiedHostsAreOutsideNames`, `api.TestOutsideNameAtAClusterAddressIsNotProxied` |
| R07 | `discovery.TestOutsideNameAtAClusterAddressIsRefused`, `controller.TestDiscoveryConnectsLikeTheGateway` |
| R08 | `scope.TestExternalNameToAnOutsideNameIsRefusedWithClusterRanges`, `scripts/e2e-kind.sh` |
| R09 | `cmd/gateway` `TestClusterEgressFlags`, chart `operator_scope_test.yaml` |
| R10 | `egress.TestServiceNamesResolveInTheClusterZone`, `egress.TestShortNameWithoutAServiceIsAnOutsideName`, `egress.TestClusterDomainFromTheResolverConfiguration`, `scripts/e2e-kind.sh` |

## Rollout

R01 to R05 apply to every install. R06 to R08 and R10 apply only with
`global.endpoints.clusterCIDRs`; removing the value turns them off. Release
notes: the three upgrade breaks in the spec's Compatibility section.

## Risks

- A legitimate Service without a selector in front of in-cluster pods is
  refused with cluster ranges. The message says to give the Service a
  selector.
- A cluster whose DNS domain is neither in the resolver's search path nor
  `cluster.local` gets `502` for full Service names with cluster ranges.
  The e2e runs with the ranges configured, so the common case is covered.
- The Operator's cache holds every Service and EndpointSlice of the
  cluster; in very large clusters that is memory to watch.
- A transport reuses connections by host without asking `DialContext`
  again. `ClusterTransport` therefore keeps two pools, one for outside
  names and one for the cluster's own names and addresses, and picks by
  the request's classification
  (`egress.TestConnectionsAreNotReusedAcrossClassifications`).
- A forged EndpointSlice with the controller's label lasts until
  Kubernetes removes it (spec non-goals).
