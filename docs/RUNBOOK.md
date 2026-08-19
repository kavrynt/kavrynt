# Kavrynt Kubernetes Operator Runbook

This runbook validates the Operator MVP.

## Local Static Validation

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
helm lint charts/k8s-operator
helm template k8s-operator charts/k8s-operator
kubectl kustomize config
```

## Private Image

Build a private beta image locally:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=0.1.0-beta.1 \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t ghcr.io/kavrynt/operator:beta-01 \
  -t kavrynt/operator:beta-01 .
```

Create a private GHCR pull secret before installing the Operator:

```bash
kubectl create namespace kavrynt-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret docker-registry ghcr-kavrynt \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password="$CR_PAT" \
  --docker-email=<email> \
  --namespace kavrynt-system
```

## Local Cluster Flow

Start Registry:

```bash
cd ../registry
go run . --addr :8081 --data /tmp/kavrynt-registry-operator.json
```

Apply the CRD and RBAC:

```bash
cd ../k8s-operator
kubectl apply -f config/crd/bases/kavrynt.io_mcpservers.yaml
kubectl apply -f config/rbac/service_account.yaml
kubectl apply -f config/rbac/role.yaml
kubectl apply -f config/rbac/role_binding.yaml
```

Run the Operator locally:

```bash
KAVRYNT_REGISTRY_URL=http://localhost:8081 go run .
```

Apply a sample MCP server:

```bash
kubectl apply -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
kubectl describe mcpserver example-mcp-server
```

Verify Registry received the record:

```bash
curl -fsS http://localhost:8081/v1/servers/example-mcp-server
```

## Helm Install

```bash
helm install k8s-operator charts/k8s-operator \
  --namespace kavrynt-system \
  --create-namespace \
  --set config.registryURL=http://registry.default.svc.cluster.local:8080 \
  --set 'imagePullSecrets[0].name=ghcr-kavrynt'
```

Check:

```bash
kubectl -n kavrynt-system rollout status deployment/k8s-operator-k8s-operator
kubectl get crd mcpservers.kavrynt.io
```

Uninstall:

```bash
helm uninstall k8s-operator -n kavrynt-system
kubectl delete crd mcpservers.kavrynt.io
```

## Troubleshooting

If status shows `Registered=False`:

- confirm Registry is reachable from the Operator pod,
- confirm `config.registryURL` points to the Registry service,
- inspect Operator logs,
- verify the `MCPServer` spec satisfies Registry validation.

If resources are stuck deleting:

- the finalizer is waiting for Registry delete to succeed,
- fix Registry connectivity and let reconcile retry,
- remove the finalizer manually only if the Registry state is already cleaned up.
