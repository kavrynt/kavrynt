# KavryntGatewayRouteSyncFailing

## Meaning

A Gateway replica has failed to sync its routes from the Kubernetes API for
10 minutes without a single success
(`kavrynt_gateway_route_sync_failure_total` grows,
`kavrynt_gateway_route_sync_success_total` does not).

## Impact

Critical. The replica keeps serving its last known routes, but new, changed,
and deleted `MCPServer` resources are not applied: new servers are not
reachable and deleted ones may still be routed.

## Diagnosis

```bash
kubectl -n kavrynt-system logs <gateway pod> --tail=200 | grep -i -E 'sync|watch|forbidden'
kubectl auth can-i list mcpservers.kavrynt.io -A \
  --as=system:serviceaccount:kavrynt-system:kavrynt-gateway
kubectl get crd mcpservers.kavrynt.io
```

- `forbidden`: the Gateway's RBAC is missing or was changed; with
  `global.watchNamespaces`, the per-namespace Roles must exist.
- CRD missing or an older schema: see the chart README, "Upgrading".
- API server errors or timeouts: the control plane is overloaded or
  unreachable from the pod.

## Mitigation

Restore RBAC by re-running `helm upgrade` with the same values, or restore the
CRD. Restarting the replica forces a full resync:
`kubectl -n kavrynt-system delete pod <gateway pod>`.
