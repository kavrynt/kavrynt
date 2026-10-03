# Kavrynt Helm Chart

This chart installs the Kavrynt Kubernetes runtime:

- Gateway (watches `MCPServer` resources read-only and routes MCP traffic)
- Kubernetes Operator (validates `MCPServer` resources, sets `Accepted`/`Ready`)
- the `MCPServer` CRD (`crds/` of the operator subchart)

Component subcharts are vendored under `charts/`, so no `helm dependency build`
is needed. Install from this repository:

```bash
helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

Install from the published OCI chart after a tagged release:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 0.0.2-beta.1 \
  --namespace kavrynt-system \
  --create-namespace
```

Authenticate with `helm registry login ghcr.io` first when the chart package
is private.

Helm installs CRDs only on first install and never upgrades them. Before
`helm upgrade`, apply the CRD from the target version's chart
(`charts/k8s-operator/crds/`) with `kubectl apply --server-side`.

Check the runtime:

```bash
kubectl get pods -n kavrynt-system
kubectl get svc -n kavrynt-system
```

Expected service: `kavrynt-gateway`.

Uninstall:

```bash
helm uninstall kavrynt -n kavrynt-system
```
