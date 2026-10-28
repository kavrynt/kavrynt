# SPEC-0029 plan: MCPServer endpoint scope

## Decisions

- R02, R04, and R08 are on by default (open question 1, as recommended):
  the runtime is in beta, and the release notes list each change.
- Cross-namespace exceptions live in the chart (open question 2, as
  recommended).
- The audience list is `global.tokenExchangeAudiences`, not
  `gateway.auth.tokenExchangeAudiences` as first drafted: the Operator and
  the Gateway both read it, like `global.watchNamespaces`. R07 says so.
- The `kavryctl upgrade` report of servers that would stop being routed
  (Compatibility) is left for a later change; the conditions name them
  after the upgrade.

## Design

- **Forwarding (R01 to R03).** In `api.Handler.proxy`, before
  authentication like the dot-segment check: methods other than `POST`,
  `GET`, and `DELETE` get `405` with `Allow: GET, POST, DELETE`. After the
  route lookup: a path suffix gets `404` unless the route has
  `ForwardSubpaths`, from `spec.forwardSubpaths` (both CRD copies). Neither
  reaches the server.
- **Endpoint namespaces (R04 to R06).** `internal/scope` (shared by the Operator and the Gateway) decides
  which namespace an endpoint host reaches, from the host alone, the way
  in-cluster DNS resolves it:
  - an IP literal: refused when inside `clusterCIDRs` (R06), otherwise not
    a namespace (the egress policy applies);
  - `<x>.<namespace>.pod[.<domain>]`: the IP in `<x>` like an IP literal,
    since the namespace label of pod names is not checked by every DNS
    setup;
  - a name with a `svc` label after its second label: the label before
    `svc` (covers headless `<pod>.<service>.<namespace>.svc`);
  - `<service>.<namespace>`: that namespace when it exists (the Operator
    may `get` namespaces); otherwise an external name;
  - one label: the Gateway's namespace, where such names resolve;
  - anything else: external.
  A namespace other than the server's own, and not allowed by a
  `crossNamespace` pair, makes `Ready` `False` with `EndpointNotAllowed`.
  The message names both namespaces, never an address.
- **Audiences (R07, R08).** `scope.Audiences` maps a namespace to its
  allowed audiences, with `*` for all. The Operator sets `Ready` `False`
  with `AudienceNotAllowed`; the Gateway checks the route's namespace
  before exchanging and answers `503` (`exchange_failed`) without calling
  the authorization server.
- **Values and flags.** `global.endpoints.crossNamespace` (list of `from`,
  `to`), `global.endpoints.clusterCIDRs` (CIDRs), and
  `global.tokenExchangeAudiences` (namespace to list of URIs), in the
  umbrella schema. The Operator gets `--endpoint-cross-namespace`,
  `--cluster-cidrs`, and `--token-exchange-audiences` (JSON), and the
  Gateway `--token-exchange-audiences`. The Operator's ClusterRole gains
  `get` on namespaces.

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `api.TestOnlyMCPMethodsAreForwarded`, e2e |
| R02, R03 | `api.TestSubpathsAreForwardedOnlyWhenAllowed`, `crd_test.yaml`, e2e |
| R04 | `scope.TestEndpointNamespaces`, `controller.TestEndpointsInOtherNamespacesAreNotReady`, e2e |
| R05 | `scope.TestCrossNamespaceExceptions`, `operator_scope_test.yaml` |
| R06 | `scope.TestClusterAddresses`, `operator_scope_test.yaml` |
| R07 | `scope.TestAudiences`, `operator_scope_test.yaml` |
| R08 | `controller.TestUnlistedAudiencesAreNotReady`, `api.TestExchangeOnlyForAllowedAudiences`, e2e |

## Rollout and risks

- Upgrades stop routing servers that use sub-paths, reach another
  namespace's Services, or ask for unlisted audiences, until the values
  allow them. The release notes list each with the value that restores it.
- An external two-label host (`example.com`) is refused when a namespace
  of that name exists, which is how in-cluster DNS would resolve it too.
