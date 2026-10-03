# Operator

Validates `MCPServer` resources (`kavrynt.io/v1alpha1`) and reports their
state. Source: `cmd/operator`, `internal/operator`, `api/v1alpha1`.

## Behaviour

- Validates each `MCPServer` with the rules in `api/v1alpha1/validation.go`
  (the CRD enforces the same rules at admission where it can).
- Sets status conditions:

| Condition | True when | False reasons |
| --- | --- | --- |
| `Accepted` | spec is valid | `InvalidSpec` |
| `Ready` | accepted and `transport: http` (the Gateway can route it) | `InvalidSpec`, `UnsupportedTransport` |

- Removes the legacy `mcpservers.kavrynt.io/registry-sync` finalizer and
  `Registered` condition left by `0.0.1-beta.1`.
- Does not deploy MCP server workloads, manage secrets, or call external
  services.

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--metrics-bind-address` | `:8080` | controller-runtime metrics |
| `--health-probe-bind-address` | `:8081` | `/healthz`, `/readyz` |
| `--leader-elect` | `false` (chart: `true`) | Leader election through a Lease |

## Manifests

- CRD: `config/crd/bases/kavrynt.io_mcpservers.yaml`, copied to
  `charts/kavrynt/charts/k8s-operator/crds/`. Keep both identical; the release
  contract check enforces it.
- Raw kustomize manifests: `config/`. Sample: `config/samples/`.

## Run locally

```bash
kubectl apply -f config/crd/bases/kavrynt.io_mcpservers.yaml
go run ./cmd/operator   # uses your current kubeconfig context
kubectl apply -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
```
