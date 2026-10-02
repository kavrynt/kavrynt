# Kavrynt

[![QA](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml/badge.svg)](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml)

> [!IMPORTANT]
> This is the private source and release repository for the commercial Kavrynt
> MCP Control Plane runtime: `kavryctl`, Gateway, Operator, Registry, the
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
- let the Operator reconcile desired state into Registry;
- route MCP traffic through Gateway;
- inspect and manage the platform with `kubectl` and kavryctl.

> [!IMPORTANT]
> Kavrynt is currently an early MVP. The core registration and HTTP routing
> loop works, but authentication, policy enforcement, durable production
> storage, and high availability are not implemented yet. Run this version only
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
                         Kavrynt control plane

  Platform engineer                                      Developer
          |                                                   |
          | kubectl apply MCPServer                           | kavryctl
          v                                                   v
  +-------------------+                             +-------------------+
  | Kavrynt Operator  |---------------------------->| Kavrynt Registry |
  +-------------------+     reconcile metadata      +-------------------+
                                                            |
                                                            | route sync
                                                            v
                                                    +-------------------+
  MCP client / agent ------------------------------>| Kavrynt Gateway  |
                                                    +-------------------+
                                                            |
                                                            | MCP request
                                                            v
                                                    +-------------------+
                                                    | Customer MCP     |
                                                    | servers          |
                                                    +-------------------+
```

Kavrynt does not deploy customer MCP server workloads in the current MVP. It
registers and routes to endpoints that already exist.

## Components

| Component | Responsibility |
| --- | --- |
| `kavryctl` | Validate manifests and manage local or remote Registry records |
| `registry` | Store MCP server metadata and expose the Registry API |
| `gateway` | Synchronize Registry state and proxy HTTP MCP requests |
| `operator` | Reconcile Kubernetes `MCPServer` resources into Registry |

All components live in this repository, build from one Go module, and are
released together under one version. The Registry is scheduled for removal in
`0.0.2-beta.1` ([ADR-0002](docs/ADR-0002-Remove-In-Cluster-Registry.md)).

The current MVP supports:

- Kubernetes-native `MCPServer` registration;
- direct registration with kavryctl and the Registry API;
- periodic Gateway synchronization from Registry;
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
export KAVRYNT_VERSION=0.0.1-beta.1

helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --create-namespace
```

The command pulls the versioned OCI Helm chart and installs the matching
Registry, Gateway, and Operator images. Authenticate Helm to GHCR first when
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

For source development, build the chart dependencies and install the local
umbrella chart:

```bash
helm dependency build --skip-refresh charts/kavrynt
helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

### Verify the installation

```bash
helm status kavrynt --namespace kavrynt-system

kubectl rollout status deployment/kavrynt-registry \
  --namespace kavrynt-system --timeout=120s

kubectl rollout status deployment/kavrynt-gateway \
  --namespace kavrynt-system --timeout=120s

kubectl rollout status deployment/kavrynt-operator \
  --namespace kavrynt-system --timeout=120s

kubectl get pods,services --namespace kavrynt-system
kubectl get crd mcpservers.kavrynt.io
```

Expected control-plane workloads:

```text
kavrynt-registry
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
  -- -listen=:8080 -text='{"mock":true,"service":"example-mcp-server"}'

kubectl expose deployment example-mcp-server \
  --port=8080 \
  --target-port=8080

kubectl rollout status deployment/example-mcp-server --timeout=120s
```

### 2. Register it through Kubernetes

```bash
kubectl apply -f operator/config/samples/kavrynt_v1alpha1_mcpserver.yaml

kubectl get mcpservers
kubectl describe mcpserver example-mcp-server
```

The Operator writes a namespace-qualified record into Registry. The example
resource becomes `default.example-mcp-server`, so equal server names in other
namespaces remain distinct.

### 3. Inspect Registry

```bash
kubectl port-forward \
  --namespace kavrynt-system \
  service/kavrynt-registry 18081:8080
```

In another terminal:

```bash
curl -fsS http://localhost:18081/readyz
curl -fsS http://localhost:18081/v1/servers
curl -fsS http://localhost:18081/v1/servers/default.example-mcp-server
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
kubectl delete -f operator/config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl delete service example-mcp-server
kubectl delete deployment example-mcp-server
```

## Use kavryctl

kavryctl supports both local file-backed development and a remote Registry.
The current canonical CLI does not install or upgrade the Kubernetes control
plane; Helm owns that release path.

The same CLI also validates and manages MCP server registrations. Install it
from source when developing locally:

```bash
go install ./cmd/kavryctl
kavryctl version
```

Validate and register a manifest against the in-cluster Registry port-forward:

```bash
kavryctl validate examples/kavryctl/mcp-server.json

kavryctl register \
  --registry http://localhost:18081 \
  examples/kavryctl/mcp-server.json

kavryctl list --registry http://localhost:18081
kavryctl inspect --registry http://localhost:18081 example-mcp-server
kavryctl unregister --registry http://localhost:18081 example-mcp-server
```

For local-only workflows, omit `--registry`. State is stored in
`.kavrynt/registry.json` by default.

See [docs/components/kavryctl](docs/components/kavryctl/README.md) for all
available commands.

## Configuration

Override component settings through Helm:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --set registry.image.tag=<version> \
  --set gateway.image.tag=<version> \
  --set operator.image.tag=<version>
```

Important values:

| Value | Default | Purpose |
| --- | --- | --- |
| `registry.image.repository` | `kavrynt/registry` | Registry container repository |
| `registry.image.tag` | `0.0.1-beta.1` | Registry image tag |
| `gateway.image.repository` | `kavrynt/gateway` | Gateway container repository |
| `gateway.image.tag` | `0.0.1-beta.1` | Gateway image tag |
| `gateway.config.registryURL` | In-cluster Registry service | Registry synchronization endpoint |
| `operator.image.repository` | `kavrynt/operator` | Operator container repository |
| `operator.image.tag` | `0.0.1-beta.1` | Operator image tag |
| `operator.config.registryURL` | In-cluster Registry service | Operator reconciliation endpoint |

Review [`charts/kavrynt/values.yaml`](charts/kavrynt/values.yaml) and each
component chart before production-oriented customization.

## Operate Kavrynt

### Health and readiness

Registry and Gateway expose:

```text
GET /healthz
GET /readyz
GET /version
GET /metrics
```

Gateway readiness remains false until it has synchronized Registry at least
once.

### Inspect desired and reconciled state

```bash
kubectl get mcpservers --all-namespaces
kubectl get mcpserver example-mcp-server -o yaml
kubectl logs --namespace kavrynt-system deployment/kavrynt-operator
kubectl logs --namespace kavrynt-system deployment/kavrynt-registry
kubectl logs --namespace kavrynt-system deployment/kavrynt-gateway
```

Component runbooks:

- [kavryctl runbook](docs/components/kavryctl/RUNBOOK.md)
- [Registry runbook](docs/components/registry/RUNBOOK.md)
- [Gateway runbook](docs/components/gateway/RUNBOOK.md)
- [Operator runbook](docs/components/operator/RUNBOOK.md)

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

- Registry and Gateway do not authenticate clients.
- Registry and Operator communication is not authenticated or encrypted by
  Kavrynt.
- Gateway does not yet enforce actor, tool, or policy authorization.
- Registry uses a local JSON file on an `emptyDir` volume by default; Registry
  data is lost if its pod is replaced.
- Registry and Gateway are single-replica by default.
- Audit, policy, approval, and usage services are not part of the current
  runtime beta.

Keep Registry and Gateway as internal `ClusterIP` services, restrict cluster
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
cmd/<component>/     entry points: kavryctl, gateway, operator, registry
internal/<component>/ component packages
build/Dockerfile     all runtime images (--target gateway|operator|registry)
charts/kavrynt/      the Helm chart, with vendored component subcharts
config/              generated CRD and RBAC manifests
test/e2e/            Kind end-to-end tests
docs/                ADRs, runbooks, component docs
```

Repository-specific engineering and contribution rules are documented in
[`AGENTS.md`](AGENTS.md).

The coordinated beta publication process is documented in
[`docs/RELEASE-0.0.1-BETA.md`](docs/RELEASE-0.0.1-BETA.md).

## Repository model

This private repository owns all customer runtime code, the Helm chart, QA,
releases, integration tests, and trial runbooks. The former `kavryctl`,
`registry`, `gateway`, and `operator` repositories are archived; their history
is imported here. `kavrynt-cloud` owns the commercial hosted control plane, and
`kavrynt-platform` owns Azure infrastructure.

## Roadmap

The next product milestones are:

1. remove the in-cluster Registry in favour of the `MCPServer` API
   ([ADR-0002](docs/ADR-0002-Remove-In-Cluster-Registry.md));
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
