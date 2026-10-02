# Upgrade To 0.0.2-beta.1

`0.0.2-beta.1` removes the in-cluster Registry
([ADR-0002](ADR-0002-Remove-In-Cluster-Registry.md)). `MCPServer` resources in
the Kubernetes API become the only source of truth. Existing `MCPServer`
resources keep working and keep their Gateway routes.

> [!IMPORTANT]
> `0.0.2-beta.1` is not published yet. These steps are validated end to end by
> `make e2e-upgrade` against locally built images.

## What changes

| Area | 0.0.1-beta.1 | 0.0.2-beta.1 |
| --- | --- | --- |
| Deployments | `kavrynt-registry`, `kavrynt-gateway`, `kavrynt-operator` | `kavrynt-gateway`, `kavrynt-operator` |
| Readiness condition | `Registered` | `Accepted` and `Ready` |
| Status fields | `registrySyncedAt`, `registryError` | removed |
| Finalizer | `mcpservers.kavrynt.io/registry-sync` | none (removed automatically) |
| Gateway routes | polled from Registry every 10 s | Kubernetes watch, read-only RBAC |
| `kavryctl` | local file or Registry URL (`--home`, `--registry`) | kubeconfig (`--context`, `-n`, `-A`); `init` removed |
| Chart values | `registry.*`, `*.config.registryURL`, `gateway.config.syncInterval`, `operator.config.syncRetryPeriod` | removed; new `gateway.config.watchNamespaces`, `gateway.rbac.create` |
| Endpoint validation | none at admission | absolute `http`/`https` URL without credentials |

## Prerequisites

- `kubectl` and Helm access to the cluster with permission to update CRDs.
- Check existing endpoints. The new CRD rejects endpoints with embedded
  credentials or non-HTTP schemes on the next write:

```bash
kubectl get mcpservers -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\t"}{.spec.endpoint}{"\n"}{end}'
```

## Steps

1. Apply the new CRD (Helm does not upgrade CRDs):

   ```bash
   export KAVRYNT_VERSION=0.0.2-beta.1
   helm pull oci://ghcr.io/kavrynt/charts/kavrynt --version "$KAVRYNT_VERSION" --untar --untardir kavrynt-chart
   kubectl apply --server-side --force-conflicts \
     -f kavrynt-chart/kavrynt/charts/k8s-operator/crds/
   ```

2. Upgrade the release. Remove `registry.*`, `registryURL`, `syncInterval`,
   and `syncRetryPeriod` from any custom values file first.

   ```bash
   helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
     --version "$KAVRYNT_VERSION" \
     --namespace kavrynt-system \
     --wait
   ```

## Verify

```bash
kubectl -n kavrynt-system get deployments
# Expected: kavrynt-gateway and kavrynt-operator only.

kubectl wait mcpserver --all -A --for=condition=Ready --timeout=120s
kubectl get mcpservers -A
# READY column shows True for http servers.

kubectl get mcpservers -A -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.finalizers}{"\n"}{end}'
# Expected: no mcpservers.kavrynt.io/registry-sync finalizers.
```

Then check routes through the Gateway:

```bash
kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080
curl -fsS http://127.0.0.1:18080/v1/routes
```

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| `Ready=False`, reason `UnsupportedTransport` | `stdio` server | Expected: the Gateway routes only `http` servers |
| `Ready=False`, reason `InvalidSpec` | Spec failed validation | `kubectl describe mcpserver <name>` shows the message; fix the spec |
| `MCPServer` delete hangs | Old operator still running or CRD not upgraded | Confirm the operator image is `0.0.2-beta.1`; it removes the legacy finalizer on reconcile |
| Gateway `/readyz` returns 503 | `MCPServer` cache not synced | Check `kubectl -n kavrynt-system logs deploy/kavrynt-gateway` for RBAC errors; `gateway.rbac.create` must be `true` unless you supply RBAC |
| `kavryctl: unknown command "init"` | Local Registry mode removed | Use `kavryctl register -n <ns> <manifest>` against a cluster |

## Rollback

Rollback is not covered by automated tests yet. Expected behaviour: rolling
back to `0.0.1-beta.1` with `helm rollback` restores the Registry,
which starts empty. The old operator re-adds its finalizer and re-syncs every
`MCPServer` into the Registry on its next reconcile. The newer CRD stays
compatible with the old operator because it only removed status fields the
old operator writes; those writes are pruned. Prefer rolling forward.
