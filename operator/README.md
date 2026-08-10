# Kavrynt Kubernetes Operator

Kavrynt Kubernetes Operator reconciles Kubernetes `MCPServer` custom resources
into Kavrynt Registry records.

This gives client engineers a Kubernetes-native workflow:

```text
kubectl apply -f mcpserver.yaml
        |
        v
Kavrynt Operator
        |
        v
Kavrynt Registry
        |
        v
Kavrynt Gateway route table
```

## MVP Scope

Included:

- `MCPServer` CRD under `kavrynt.io/v1alpha1`.
- Controller that watches `MCPServer` resources.
- Registry upsert on create/update.
- Registry delete on resource deletion through a finalizer.
- Status condition showing Registry sync state.
- Raw Kubernetes manifests, Helm chart, Dockerfile, GitHub Actions QA, and runbook.

Not included yet:

- Deployment of MCP server workloads.
- Secret injection for MCP server credentials.
- Gateway configuration objects.
- multi-tenant authorization.
- admission webhooks.
- conversion webhooks.
- registry authentication.

## Example

```yaml
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: example-mcp-server
  namespace: default
spec:
  version: 0.1.0
  transport: http
  endpoint: http://example-mcp-server.default.svc.cluster.local:8080
```

Apply:

```bash
kubectl apply -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
kubectl describe mcpserver example-mcp-server
```

## Validate

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
helm lint charts/k8s-operator
helm template k8s-operator charts/k8s-operator
kubectl kustomize config
```

## Run Locally Against A Cluster

```bash
KAVRYNT_REGISTRY_URL=http://localhost:8081 go run .
```

The Operator needs Kubernetes API access through the current kubeconfig.
