# Kavrynt

[![QA](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml/badge.svg)](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml)

> [!IMPORTANT]
> This is the private source and release repository for the commercial Kavrynt
> MCP Control Plane runtime: `kavryctl`, Gateway, Operator, the
> `MCPServer` CRD, and the Helm chart. See
> [ADR-0001](docs/ADR-0001-Runtime-Monorepo.md).

Kavrynt is a Kubernetes-native control plane for registering, discovering, and
routing Model Context Protocol (MCP) servers.

Platform engineers install Kavrynt once in a Kubernetes cluster. Developers can
then register MCP servers through Kubernetes resources or kavryctl, while
agents and MCP clients reach those servers through one consistent Gateway.

Kavrynt is designed to operate like other Kubernetes platform products:

- install and operate the control plane with `kavryctl`;
- declare MCP servers as Kubernetes resources;
- let the Operator validate each server and report its readiness;
- route MCP traffic through Gateway;
- inspect and manage the platform with `kubectl` and kavryctl.

> [!IMPORTANT]
> Kavrynt is currently an early MVP. The core registration and HTTP routing
> loop works, but authentication, policy enforcement, and high availability are
> not implemented yet. Run this version only
> in trusted development or evaluation clusters.

## Why Kavrynt

MCP makes tools available to AI applications, but operating many MCP servers
creates platform concerns that individual application teams should not have to
solve repeatedly:

- Where are MCP servers running?
- Which endpoint and transport should a client use?
- How is configuration kept consistent with Kubernetes?
- How can operators inspect the available routes?
- Where should future authentication, policy, audit, and usage controls live?

Kavrynt provides the infrastructure layer for those concerns without requiring
each MCP server to become its own platform.

## Architecture

```text
  Platform engineer / developer
          |
          | kubectl apply MCPServer  |  kavryctl register
          v
  +---------------------------+   validate, set Accepted/Ready
  | Kubernetes API            |<------------------------------- Kavrynt Operator
  | MCPServer resources       |
  +---------------------------+
          |
          | watch (read-only)
          v
  +-------------------+
  | Kavrynt Gateway   |<-------------- MCP client / agent
  +-------------------+   /mcp/<namespace>.<name>
          |
          | MCP request
          v
  +-------------------+
  | Customer MCP      |
  | servers           |
  +-------------------+
```

Kavrynt does not deploy customer MCP server workloads in the current MVP. It
registers and routes to endpoints that already exist.

## Components

| Component | Responsibility |
| --- | --- |
| `kavryctl` | Validate manifests and register, list, inspect, and remove `MCPServer` resources |
| `operator` | Validate `MCPServer` resources and report `Accepted` and `Ready` conditions |
| `gateway` | Watch `Ready` `MCPServer` resources and proxy HTTP MCP requests |

All components live in this repository, build from one Go module, and are
released together under one version. The `MCPServer` API is the in-cluster
source of truth; the separate Registry service was removed in `0.0.2-beta.1`
([ADR-0002](docs/ADR-0002-Remove-In-Cluster-Registry.md)).

The current MVP supports:

- Kubernetes-native `MCPServer` registration;
- registration with kavryctl through the Kubernetes API;
- Gateway route updates on Kubernetes watch events;
- HTTP request proxying through `/mcp/<server-id>`;
- health, readiness, version, metrics, and route inspection endpoints.

Manifests can describe `stdio` servers, but Gateway runtime proxying currently
supports HTTP endpoints only.

## Install on Kubernetes

### Prerequisites

- A conformant Kubernetes cluster.
- Helm 3.
- `kubectl` access with permission to create CRDs, Deployments, Services,
  ServiceAccounts, ClusterRoles, and ClusterRoleBindings.

Kavrynt can run on managed or self-managed conformant Kubernetes clusters,
including GKE, EKS, AKS, OpenShift, and local Kind clusters.

### Install the control plane

```bash
export KAVRYNT_VERSION=0.0.2-beta.1

helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --create-namespace
```

The command pulls the versioned OCI Helm chart and installs the matching
Gateway and Operator images. Authenticate Helm to GHCR first when
the chart package is private. The default release is `kavrynt` in the
`kavrynt-system` namespace.

Use a values file or repeatable `--set` flags to customize the installation:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --create-namespace \
  --values kavrynt-values.yaml \
  --set gateway.replicaCount=2
```

For source development, install the local chart (component subcharts are
vendored, so no dependency build is needed):

```bash
helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

### Verify the installation

```bash
helm status kavrynt --namespace kavrynt-system

kubectl rollout status deployment/kavrynt-gateway \
  --namespace kavrynt-system --timeout=120s

kubectl rollout status deployment/kavrynt-operator \
  --namespace kavrynt-system --timeout=120s

kubectl get pods,services --namespace kavrynt-system
kubectl get crd mcpservers.kavrynt.io
```

Expected control-plane workloads:

```text
kavrynt-gateway
kavrynt-operator
```

## Try the complete product loop

The following smoke test deploys an HTTP echo workload, registers it as an
`MCPServer`, and reaches it through Kavrynt Gateway. The echo workload verifies
routing; it is not a full MCP implementation.

### 1. Deploy a test backend

```bash
kubectl create deployment example-mcp-server \
  --image=hashicorp/http-echo:1.0 \
  -- /http-echo -listen=:8080 -text='{"mock":true,"service":"example-mcp-server"}'

kubectl expose deployment example-mcp-server \
  --port=8080 \
  --target-port=8080

kubectl rollout status deployment/example-mcp-server --timeout=120s
```

### 2. Register it through Kubernetes

```bash
kubectl apply -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl wait mcpserver/example-mcp-server --for=condition=Ready --timeout=60s

kubectl get mcpservers
kubectl describe mcpserver example-mcp-server
```

The Operator sets the `Ready` condition once the spec is valid and uses the
`http` transport. The Gateway route is namespace-qualified: the example becomes
`default.example-mcp-server`, so equal server names in other namespaces remain
distinct.

### 3. Inspect registrations

```bash
kavryctl list -A
kavryctl inspect -n default example-mcp-server
```

### 4. Route through Gateway

```bash
kubectl port-forward \
  --namespace kavrynt-system \
  service/kavrynt-gateway 18080:8080
```

In another terminal:

```bash
curl -fsS http://localhost:18080/readyz
curl -fsS http://localhost:18080/v1/routes
curl -fsS http://localhost:18080/mcp/default.example-mcp-server
```

The final request travels through Gateway to the registered workload and should
return:

```json
{"mock":true,"service":"example-mcp-server"}
```

### 5. Clean up the example

```bash
kubectl delete -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl delete service example-mcp-server
kubectl delete deployment example-mcp-server
```

## Use kavryctl

kavryctl manages `MCPServer` resources through your kubeconfig. It does not
install or upgrade the control plane; Helm owns that release path. Install it
from source when developing locally:

```bash
go install ./cmd/kavryctl
kavryctl version
```

Validate, register, inspect, and remove a server:

```bash
kavryctl validate examples/kavryctl/mcp-server.yaml
kavryctl register -n default examples/kavryctl/mcp-server.yaml
kavryctl list -A
kavryctl inspect -n default example-mcp-server
kavryctl unregister -n default example-mcp-server
```

All cluster commands accept `--kubeconfig`, `--context`, and `-n/--namespace`.
Manifests may be YAML or JSON.

See [docs/components/kavryctl](docs/components/kavryctl/README.md) for all
available commands.

## Configuration

Override component settings through Helm:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --set gateway.image.tag=<version> \
  --set operator.image.tag=<version>
```

Important values:

| Value | Default | Purpose |
| --- | --- | --- |
| `gateway.image.repository` | `kavrynt/gateway` | Gateway container repository |
| `gateway.image.tag` | `0.0.2-beta.1` | Gateway image tag |
| `gateway.config.watchNamespaces` | `[]` (all) | Namespaces the Gateway watches; a list switches RBAC to per-namespace Roles |
| `gateway.rbac.create` | `true` | Create read-only `MCPServer` RBAC for the Gateway |
| `operator.image.repository` | `kavrynt/operator` | Operator container repository |
| `operator.image.tag` | `0.0.2-beta.1` | Operator image tag |

Review [`charts/kavrynt/values.yaml`](charts/kavrynt/values.yaml) and each
component chart before production-oriented customization.

## Operate Kavrynt

### Health and readiness

Gateway exposes (the Operator serves `/healthz` and `/readyz` on port 8081):

```text
GET /healthz
GET /readyz
GET /version
GET /metrics
```

Gateway readiness remains false until its `MCPServer` cache has synced once.

### Inspect desired and reconciled state

```bash
kubectl get mcpservers --all-namespaces
kubectl get mcpserver example-mcp-server -o yaml
kubectl logs --namespace kavrynt-system deployment/kavrynt-operator
kubectl logs --namespace kavrynt-system deployment/kavrynt-gateway
```


### Uninstall

```bash
helm uninstall kavrynt --namespace kavrynt-system
```

Helm keeps the `MCPServer` CRD and its custom resources. After confirming that
cluster-scoped data can be removed, delete the CRD and namespace explicitly:

```bash
kubectl delete crd mcpservers.kavrynt.io
kubectl delete namespace kavrynt-system
```

## Current security and durability boundaries

The MVP uses defensive container defaults: non-root users, dropped Linux
capabilities, read-only root filesystems, and limited Kubernetes RBAC. However,
the current release is not production-security complete:

- Gateway does not authenticate clients.
- Gateway never forwards `Authorization`, `Cookie`, or `Proxy-Authorization`
  to MCP servers, and drops upstream `Set-Cookie`. It does not yet exchange
  tokens for upstream servers (ADR-0003).
- Gateway does not yet enforce actor, tool, or policy authorization.
- Gateway has read-only Kubernetes access to `MCPServer` resources and nothing
  else.
- Gateway is single-replica by default.
- Audit, policy, approval, and usage services are not part of the current
  runtime beta.

Keep Gateway as an internal `ClusterIP` service, restrict cluster
access, and do not expose this MVP directly to untrusted networks.

## Development

Prerequisites:

- Go 1.26+
- Docker
- Helm 3.x or 4.x
- `kubectl`
- Kind for disposable Kubernetes testing

Run repository validation:

```bash
make qa
```

Useful targets:

```bash
make test
make vet
make lint
make sast
make vulncheck
make fmt-check
make build
make docker-build
make helm-lint
make helm-template
make e2e-kind
```

Repository layout:

```text
api/v1alpha1/        MCPServer CRD Go types
cmd/<component>/     entry points: kavryctl, gateway, operator
internal/<component>/ component packages
build/Dockerfile     all runtime images (--target gateway|operator)
charts/kavrynt/      the Helm chart, with vendored component subcharts
config/              generated CRD and RBAC manifests
test/e2e/            Kind end-to-end tests
docs/                ADRs, runbooks, component docs
```

Repository-specific engineering and contribution rules are documented in
[`AGENTS.md`](AGENTS.md).

The release process is documented in [`docs/RELEASE.md`](docs/RELEASE.md).

## Repository model

This private repository owns all customer runtime code, the Helm chart, QA,
releases, integration tests, and trial runbooks. The former `kavryctl`,
`registry`, `gateway`, and `operator` repositories are archived; their history
is imported here. `kavrynt-cloud` owns the commercial hosted control plane, and
`kavrynt-platform` owns Azure infrastructure.

## Roadmap

The next product milestones are:

1. ~~remove the in-cluster Registry~~ (done in `0.0.2-beta.1`,
   [ADR-0002](docs/ADR-0002-Remove-In-Cluster-Registry.md));
2. Gateway authentication and no token passthrough
   ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md));
3. MCP Streamable HTTP and protocol-aware routing;
4. policy decisions and tool-level authorization;
5. audit and usage events;
6. upgrade and rollback commands in kavryctl;
7. upgrade, rollback, backup, and recovery workflows;
8. tested high-availability deployment profiles.

Hosted commercial management, cross-cluster inventory, enterprise identity,
and dedicated customer control planes belong to Kavrynt Cloud rather than this
runtime repository.

## Project status

Kavrynt is under active development. APIs, chart values, and storage formats
may change before the first stable release.

Use [GitHub issues](https://github.com/kavrynt/kavrynt/issues) for reproducible
bugs and feature requests. Please include the Kavrynt version, Kubernetes
version, installation method, relevant manifests, and sanitized logs.

## Commercial status

Kavrynt is private commercial software. Trial access is provided through
approved prerelease images, client binaries, Helm charts, and runbooks under
the applicable Kavrynt commercial terms.
