# KavryntMCPServerNotReady

## Meaning

The Operator has reported an `MCPServer` (labels `namespace`, `name`) as not
`Ready` for longer than `metrics.prometheusRule.notReadyFor` (default 10m).

## Impact

Warning. The Gateway does not route that server; agents calling
`/mcp/<namespace>.<name>` get `404`.

## Diagnosis

```bash
kubectl -n <namespace> get mcpserver <name> -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

The `Ready` condition's reason says why:

- `InvalidSpec` (and `Accepted=False`): the spec failed validation; the
  message names the field.
- `UnsupportedTransport`: `stdio` servers validate but are not routed. Run
  the server behind a Streamable HTTP bridge and register the bridge's `http`
  endpoint ([example](../../examples/stdio-bridge/filesystem.yaml)).
- `NamespaceNotWatched`: the server is outside `global.watchNamespaces`.
- `EndpointNotAllowed`: the message says which case it is.
  - The endpoint reaches another namespace (the message names both): point
    it at a Service in the server's own namespace, or add a
    `global.endpoints.crossNamespace` pair if the platform team allows it.
  - The endpoint is an address, or a pod name, in
    `global.endpoints.clusterCIDRs`: use the Service's name instead. No
    namespace pair allows an address.
  - The endpoint leads through an `ExternalName` Service to another
    namespace, or (with `global.endpoints.clusterCIDRs`) to anything but a
    Service's full name: register the Service it stands for, or the
    outside name itself, as `spec.endpoint`.
  - The endpoint's Service has an EndpointSlice that names another
    namespace, or one that Kubernetes does not manage and that lists an
    address in `global.endpoints.clusterCIDRs`: give the Service a
    selector, so Kubernetes writes its EndpointSlices, and delete the
    hand-written one (`kubectl get endpointslices -l kubernetes.io/service-name=<service>`).
  - A namespace or a Service could not be read: check the Operator's logs
    and its RBAC (`get` on namespaces; `get`, `list`, `watch` on Services;
    `list`, `watch` on EndpointSlices); the Operator checks again after 30
    seconds.
- `AudienceNotAllowed`: `spec.tokenExchange.audience` is not listed for the
  namespace in `global.tokenExchangeAudiences`.

`ToolsDiscovered=False` alone never makes a server not Ready: a server can
be `Ready` (routed) while it does not answer. The `Discovery` column of
`kubectl get mcpservers` shows that case; see
[KavryntMCPToolDiscoveryFailing](KavryntMCPToolDiscoveryFailing.md).

## Mitigation

Correct the spec (`kavryctl validate <file>` checks a manifest locally), move
the server to a watched namespace, or delete the `MCPServer` if it is not
meant to be routed.
