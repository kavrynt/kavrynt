# Kavrynt Kubernetes Operator

Kavrynt Kubernetes Operator reconciles Kubernetes `MCPServer` custom resources
into Kavrynt Registry records.

Kavrynt is an umbrella for infrastructure products. This repository is part of
the Kavrynt MCP Control Plane alongside `kavryctl`, Gateway, and Registry.

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
- Namespace-qualified Registry identities (`<namespace>.<name>`) to prevent
  collisions between Kubernetes namespaces.
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

## Image

Private beta images use GitHub Container Registry and Docker Hub:

```text
ghcr.io/kavrynt/operator:0.0.1-beta.1
docker.io/kavrynt/operator:0.0.1-beta.1
```

Build locally:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=0.0.1-beta.1 \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t ghcr.io/kavrynt/operator:0.0.1-beta.1 \
  -t docker.io/kavrynt/operator:0.0.1-beta.1 .
```

Clusters pulling private GHCR images need an image pull secret named in Helm via
`imagePullSecrets[0].name`.

## Run Locally Against A Cluster

```bash
KAVRYNT_REGISTRY_URL=http://localhost:8081 go run .
```

The Operator needs Kubernetes API access through the current kubeconfig.
