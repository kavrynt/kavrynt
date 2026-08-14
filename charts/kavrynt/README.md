# Kavrynt Helm Chart

This chart installs the Kavrynt Kubernetes control plane:

- Registry
- Gateway
- Kubernetes Operator

Install from this repository:

```bash
helm dependency build charts/kavrynt
helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

Install from the published OCI chart after a tagged release:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 0.1.0 \
  --namespace kavrynt-system \
  --create-namespace
```

Check the control plane:

```bash
kubectl get pods -n kavrynt-system
kubectl get svc -n kavrynt-system
```

Expected services:

- `kavrynt-registry`
- `kavrynt-gateway`

Uninstall:

```bash
helm uninstall kavrynt -n kavrynt-system
```
