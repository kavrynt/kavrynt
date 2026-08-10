# Kavrynt

Kavrynt is an MCP infrastructure control plane for platform teams. It provides
the core components needed to register, route, operate, and eventually govern
MCP servers across local development and Kubernetes environments.

This repository is the public product monorepo for the Kavrynt MVP.

## Components

```text
cmd/kavryctl        Developer and operator CLI
services/registry  Registry API and metadata source of truth
services/gateway   Runtime gateway for MCP traffic
operator           Kubernetes operator for MCPServer custom resources
```

Each component is currently kept as an independent Go module. The root
`go.work` file connects them for local development.

## Repository Layout

```text
.
├── cmd/
│   └── kavryctl/
├── charts/
│   └── kavrynt/
├── services/
│   ├── gateway/
│   └── registry/
├── operator/
├── .github/
│   └── workflows/
├── AGENTS.md
├── Makefile
├── go.work
└── README.md
```

## Product Install Experience

Kavrynt should feel like standard Kubernetes platform software. A developer or
platform engineer installs the control plane into one namespace, then uses
`kavryctl` and Kubernetes `MCPServer` resources to register MCP servers.

Target release experience:

```bash
curl -fsSL https://kavrynt.com/install.sh | sh

helm repo add kavrynt https://charts.kavrynt.com
helm repo update
helm upgrade --install kavrynt kavrynt/kavrynt \
  --namespace kavrynt-system \
  --create-namespace

kubectl get pods -n kavrynt-system
kavryctl version
```

Expected control-plane pods:

```text
kavrynt-registry
kavrynt-gateway
kavrynt-operator
```

Current MVP reality:

- `kavryctl` can be installed from source.
- Registry, Gateway, and Operator can be installed from this repository with
  Helm.
- Public binary releases, a public Helm repository, and stable Docker image tags
  are the next distribution work items.

## Local Development

Prerequisites:

- Go 1.23+
- Docker, for image builds
- Helm, for chart validation
- kubectl and Kind, for local Kubernetes testing

Run Go checks across all components:

```bash
make qa
```

Run only tests:

```bash
make test
```

Build all local development images:

```bash
make docker-build
```

Validate Helm charts:

```bash
make helm-lint
make helm-template
```

## Quick Start For Developers

This is the minimum path for a developer to download Kavrynt, install the CLI,
run the control-plane components in Kubernetes, and verify the system.

### 1. Clone The Repository

```bash
git clone https://github.com/kavrynt/kavrynt.git
cd kavrynt
```

### 2. Install `kavryctl`

From the repository root:

```bash
go install ./cmd/kavryctl
```

Confirm the binary is available:

```bash
kavryctl version
```

If your shell cannot find `kavryctl`, add Go's binary directory to your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### 3. Run Kavrynt Locally Without Kubernetes

Use this when you only want to test the CLI and Registry API.

Terminal 1:

```bash
cd services/registry
go run . --addr :8080 --data /tmp/kavrynt-registry.json
```

Terminal 2:

```bash
kavryctl validate cmd/kavryctl/examples/mcp-server.json
kavryctl register --registry http://localhost:8080 cmd/kavryctl/examples/mcp-server.json
kavryctl list --registry http://localhost:8080
kavryctl inspect --registry http://localhost:8080 example-mcp-server
```

Cleanup:

```bash
kavryctl unregister --registry http://localhost:8080 example-mcp-server
```

## Kubernetes Quick Start From Source

This is the current source-based MVP flow. It installs the Kavrynt control plane
into the `kavrynt-system` namespace using the umbrella Helm chart.

Prerequisites:

- Docker
- Kind
- kubectl
- Helm

### 1. Create A Kind Cluster

```bash
kind create cluster --name kavrynt-dev
kubectl cluster-info --context kind-kavrynt-dev
```

### 2. Build And Load Local Images

```bash
make docker-build

kind load docker-image kavrynt/registry:dev --name kavrynt-dev
kind load docker-image kavrynt/gateway:dev --name kavrynt-dev
kind load docker-image kavrynt/k8s-operator:dev --name kavrynt-dev
kind load docker-image kavrynt/kavryctl:dev --name kavrynt-dev
```

### 3. Install Kavrynt Control Plane

```bash
helm dependency build charts/kavrynt

helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

Wait for it:

```bash
kubectl rollout status deployment/kavrynt-registry -n kavrynt-system --timeout=120s
kubectl rollout status deployment/kavrynt-gateway -n kavrynt-system --timeout=120s
kubectl rollout status deployment/kavrynt-operator -n kavrynt-system --timeout=120s
kubectl get pods -n kavrynt-system
kubectl get svc -n kavrynt-system
kubectl get crd mcpservers.kavrynt.io
```

Expected services:

```text
kavrynt-registry
kavrynt-gateway
```

### 4. Deploy A Temporary Example MCP HTTP Server

The MVP Gateway routes to HTTP MCP endpoints registered in Registry. If you do
not already have an MCP server, deploy a temporary HTTP echo service:

```bash
kubectl create deployment example-mcp-server \
  --image=hashicorp/http-echo:1.0 \
  -- -listen=:8080 -text='{"mock":true,"service":"example-mcp-server"}'

kubectl expose deployment example-mcp-server --port=8080 --target-port=8080
kubectl rollout status deployment/example-mcp-server --timeout=120s
```

### 5. Register The MCP Server Through Kubernetes

```bash
kubectl apply -f operator/config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
kubectl describe mcpserver example-mcp-server
```

The Operator watches the `MCPServer` resource and writes the server record into
Registry.

### 6. Verify Registry And Gateway

Port-forward Registry:

```bash
kubectl port-forward -n kavrynt-system svc/kavrynt-registry 18081:8080
```

In another terminal:

```bash
curl -fsS http://localhost:18081/healthz
curl -fsS http://localhost:18081/v1/servers
curl -fsS http://localhost:18081/v1/servers/example-mcp-server
```

Port-forward Gateway:

```bash
kubectl port-forward -n kavrynt-system svc/kavrynt-gateway 18080:8080
```

In another terminal:

```bash
curl -fsS http://localhost:18080/healthz
curl -fsS http://localhost:18080/readyz
curl -fsS http://localhost:18080/v1/routes
curl -fsS http://localhost:18080/mcp/example-mcp-server
```

Expected result:

- Registry returns the `example-mcp-server` record.
- Gateway lists a route for `example-mcp-server`.
- Gateway proxies `/mcp/example-mcp-server` to the temporary example service.

### 7. Register Through `kavryctl` Instead Of Operator

You can also register directly through the Registry API from your laptop:

```bash
kavryctl register --registry http://localhost:18081 cmd/kavryctl/examples/mcp-server.json
kavryctl list --registry http://localhost:18081
kavryctl inspect --registry http://localhost:18081 example-mcp-server
```

Use `kubectl apply` when you want Kubernetes-native declaration. Use
`kavryctl register` when you want a CLI-driven workflow.

### 8. Cleanup

```bash
kubectl delete -f operator/config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl delete service example-mcp-server
kubectl delete deployment example-mcp-server

helm uninstall kavrynt -n kavrynt-system
kubectl delete namespace kavrynt-system

kind delete cluster --name kavrynt-dev
```

## Current Distribution Model

Today, developers install Kavrynt from source and local images:

- `kavryctl`: installed with `go install ./cmd/kavryctl`
- Registry, Gateway, and Operator: installed together with `charts/kavrynt`

Public release artifacts should come next:

- GitHub Releases for `kavryctl` binaries.
- Docker Hub images for Registry, Gateway, Operator, and optional `kavryctl`.
- Published Helm chart or OCI Helm chart package for `charts/kavrynt`.
- A one-command local install script after the MVP flow stabilizes.

## MVP Flow

The first end-to-end Kavrynt workflow is:

```text
Developer or platform engineer
  -> defines an MCP server manifest or MCPServer custom resource
  -> registers it through kavryctl or the Kubernetes operator
  -> Registry stores the MCP server metadata
  -> Gateway syncs Registry state
  -> MCP clients call the server through Gateway
```

## Source Repositories

This monorepo was assembled from the original private component repositories:

- `kavryctl`
- `registry`
- `gateway`
- `k8s-operator`

Those repositories should be considered pre-monorepo component sources once this
repository becomes the primary public development location.

## License

License selection is pending. Do not assume permissive reuse rights until a
license file is added.
