# Kavrynt

Kavrynt is a source-available MCP infrastructure control plane for platform
teams. It provides the core components needed to register, route, operate, and
eventually govern MCP servers across local development and Kubernetes
environments.

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

Target release experience for Linux and macOS:

```bash
curl -fsSL https://kavrynt.com/install.sh | sh

helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace

kubectl get pods -n kavrynt-system
kavryctl version
```

Target release experience for Windows PowerShell:

```powershell
iwr https://kavrynt.com/install.ps1 -UseB | iex
kavryctl version
```

Expected control-plane pods:

```text
kavrynt-registry
kavrynt-gateway
kavrynt-operator
```

Current MVP reality:

- `kavryctl` can be installed from source today. The repository includes release
  automation for Linux, macOS, and Windows binaries.
- Registry, Gateway, and Operator images are published on Docker Hub under the
  `kavrynt` organization.
- The umbrella Helm chart is available in this repository. Tagged releases are
  intended to publish the packaged chart to GHCR as an OCI Helm chart.

Published MVP images:

```text
kavrynt/kavryctl:dev
kavrynt/registry:dev
kavrynt/gateway:dev
kavrynt/k8s-operator:dev
```

The `dev` tags point at the latest pushed MVP build from `main`. Use
commit-pinned tags, such as `2c5da9c`, when you need a reproducible test. The
published images are currently `linux/amd64`. Apple Silicon users can still test
on Kind by using the local image development path below.

## Local Development

Prerequisites:

- Go 1.23+
- Docker, for image builds
- Helm, for chart validation
- kubectl, for Kubernetes testing
- Kind, only for disposable local cluster testing

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

From GitHub Releases on Linux or macOS after the first release is published:

```bash
curl -fsSL https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.sh | sh
```

Install a specific release version:

```bash
curl -fsSL https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.sh | KAVRYNT_VERSION=v0.1.0 sh
```

From PowerShell on Windows after the first release is published:

```powershell
iwr https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.ps1 -UseB | iex
```

Current MVP source install from the repository root:

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

## Install Kavrynt On Kubernetes

This is the minimum Kubernetes MVP flow. It follows the same shape as common
Kubernetes platform installs: prepare a cluster, install the control plane with
Helm, verify the workloads, deploy a sample MCP server, and remove the install
when finished.

Kavrynt is not limited to Kind. You can use EKS, AKS, GKE, OpenShift,
self-managed Kubernetes, or any conformant cluster where you have permission to
install CRDs, RBAC, Deployments, and Services. Use Kind only when you want a
throwaway local test cluster.

Prerequisites:

- kubectl
- Helm
- Git
- Docker and Kind, only for local testing

Check the tools:

```bash
kubectl version --client
helm version
git --version

# Local testing only
docker version
kind version
```

### 1. Choose A Kubernetes Cluster

For an existing Kubernetes cluster:

```bash
kubectl config current-context
kubectl get nodes
```

For local testing with Kind:

```bash
kind create cluster --name kavrynt-dev
kubectl cluster-info --context kind-kavrynt-dev
```

Expected result: `kubectl get nodes` returns at least one `Ready` node.

### 2. Clone The Chart Source

```bash
git clone https://github.com/kavrynt/kavrynt.git
cd kavrynt
```

The chart dependencies are local file dependencies in this MVP, so the source
checkout is required until the Helm chart is published as an OCI chart.

### 3. Install The Kavrynt Control Plane

The default chart values use these Docker Hub images:

```text
kavrynt/registry:dev
kavrynt/gateway:dev
kavrynt/k8s-operator:dev
```

Install the control plane:

```bash
helm dependency build charts/kavrynt

helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

Target release command, after the packaged OCI chart is published:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 0.1.0 \
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

Expected pods:

```text
kavrynt-registry
kavrynt-gateway
kavrynt-operator
```

Expected services:

```text
kavrynt-registry
kavrynt-gateway
```

### 4. Optional: Install `kavryctl`

The control plane can be tested with only `kubectl` and `curl`. Install
`kavryctl` if you also want to test the developer CLI workflow:

Linux and macOS release installer:

```bash
curl -fsSL https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.sh | sh
export PATH="$HOME/.kavrynt/bin:$PATH"
kavryctl version
```

Windows PowerShell release installer:

```powershell
iwr https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.ps1 -UseB | iex
kavryctl version
```

Current MVP source install:

```bash
go install ./cmd/kavryctl
kavryctl version
```

If your shell cannot find `kavryctl`, add Go's binary directory to your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### 5. Deploy A Temporary Example MCP HTTP Server

The MVP Gateway routes to HTTP MCP endpoints registered in Registry. If you do
not already have an MCP server, deploy a temporary HTTP echo service:

```bash
kubectl create deployment example-mcp-server \
  --image=hashicorp/http-echo:1.0 \
  -- -listen=:8080 -text='{"mock":true,"service":"example-mcp-server"}'

kubectl expose deployment example-mcp-server --port=8080 --target-port=8080
kubectl rollout status deployment/example-mcp-server --timeout=120s
```

### 6. Register The MCP Server Through Kubernetes

```bash
kubectl apply -f operator/config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
kubectl describe mcpserver example-mcp-server
```

The Operator watches the `MCPServer` resource and writes the server record into
Registry.

### 7. Verify Registry And Gateway

Port-forward Registry:

```bash
kubectl port-forward -n kavrynt-system svc/kavrynt-registry 18081:8080
```

In another terminal:

```bash
curl -fsS http://localhost:18081/healthz
curl -fsS http://localhost:18081/v1/servers
curl -fsS http://localhost:18081/v1/servers/default.example-mcp-server
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
curl -fsS http://localhost:18080/mcp/default.example-mcp-server
```

Expected result:

- Registry returns the `example-mcp-server` record.
- Gateway lists a route for `example-mcp-server`.
- Gateway proxies `/mcp/default.example-mcp-server` to the temporary example
  service. Kubernetes-originated server IDs use `<namespace>.<name>` so equal
  names in different namespaces remain distinct.

### 8. Register Through `kavryctl` Instead Of Operator

You can also register directly through the Registry API from your laptop:

```bash
kavryctl register --registry http://localhost:18081 cmd/kavryctl/examples/mcp-server.json
kavryctl list --registry http://localhost:18081
kavryctl inspect --registry http://localhost:18081 example-mcp-server
```

Use `kubectl apply` when you want Kubernetes-native declaration. Use
`kavryctl register` when you want a CLI-driven workflow.

### 9. Cleanup

```bash
kubectl delete -f operator/config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl delete service example-mcp-server
kubectl delete deployment example-mcp-server

helm uninstall kavrynt -n kavrynt-system
kubectl delete namespace kavrynt-system

kind delete cluster --name kavrynt-dev
```

## Local Image Development

Use this path when you are changing Kavrynt code and want Kind to run your local
images instead of the Docker Hub `dev` tags.

```bash
make docker-build

kind load docker-image kavrynt/registry:dev --name kavrynt-dev
kind load docker-image kavrynt/gateway:dev --name kavrynt-dev
kind load docker-image kavrynt/k8s-operator:dev --name kavrynt-dev
kind load docker-image kavrynt/kavryctl:dev --name kavrynt-dev
```

Then run the same Helm install command from the Kubernetes quick start.

## Current Distribution Model

Today, developers can install Kavrynt from source plus Docker Hub images. Tagged
releases are intended to add binary and chart distribution:

- `kavryctl`: installed with the shell/PowerShell installer or
  `go install ./cmd/kavryctl`
- Registry, Gateway, and Operator: pulled from Docker Hub and installed together
  with `charts/kavrynt` or the published OCI Helm chart

Public release artifacts should come next:

- Versioned container image publishing for Registry, Gateway, and Operator.
- Multi-architecture container images.
- `kavryctl install`, `kavryctl status`, and `kavryctl uninstall`.

## Release Automation

Releases are tag-driven. A tag such as `v0.1.0` publishes:

- `kavryctl` binaries for Linux, macOS, and Windows.
- `checksums.txt` for release artifact verification.
- The `charts/kavrynt` umbrella chart to GHCR as an OCI Helm chart.

Before tagging, run:

```bash
make qa
make helm-package
```

If GoReleaser is installed locally, check the binary packaging flow without
publishing:

```bash
make release-snapshot
```

Publish a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow publishes the Helm chart to:

```text
oci://ghcr.io/kavrynt/charts/kavrynt
```

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

Kavrynt is licensed under the Elastic License 2.0. See [LICENSE](LICENSE).

This makes the public Kavrynt MVP source-available, not permissively licensed
open source. You may use, copy, distribute, make available, and modify Kavrynt
under the license terms, but you may not provide Kavrynt to third parties as a
hosted or managed service that exposes a substantial set of Kavrynt's features.

For commercial managed-service rights, hosted SaaS/PaaS partnerships, or custom
enterprise licensing, contact `admin@kavrynt.com`.
