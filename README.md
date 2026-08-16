# Kavrynt

[![QA](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml/badge.svg)](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml)
[![License: ELv2](https://img.shields.io/badge/license-Elastic%20License%202.0-blue)](LICENSE)

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
| [kavryctl](cmd/kavryctl) | Validate manifests and manage local or remote Registry records |
| [Registry](services/registry) | Store MCP server metadata and expose the Registry API |
| [Gateway](services/gateway) | Synchronize Registry state and proxy HTTP MCP requests |
| [Operator](operator) | Reconcile Kubernetes `MCPServer` resources into Registry |

Each component is an independent Go module and container. The root [`go.work`](go.work)
connects the modules for repository development, and the umbrella Helm chart
installs the Kubernetes control plane.

The current MVP supports:

- Kubernetes-native `MCPServer` registration;
- direct registration with kavryctl and the Registry API;
- namespace-qualified server identities;
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

### Install kavryctl

Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.sh | sh
export PATH="$HOME/.kavrynt/bin:$PATH"
```

Windows PowerShell:

```powershell
iwr https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.ps1 -UseB | iex
```

### Install the control plane

```bash
kavryctl install
```

The command pulls the OCI Helm chart for the `kavryctl` release and installs
the matching versioned Registry, Gateway, and Operator images from GHCR. The
default release is `kavrynt` in the `kavrynt-system` namespace.

Use a values file or repeatable `--set` flags to customize the installation:

```bash
kavryctl install --values kavrynt-values.yaml
kavryctl install --set gateway.replicaCount=2
```

Render the same release as Kubernetes YAML for review or GitOps:

```bash
kavryctl manifest generate > kavrynt.yaml
```

For source development, point the CLI at the local umbrella chart:

```bash
go run ./cmd/kavryctl install --chart charts/kavrynt
```

### Verify the installation

```bash
kavryctl status

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

The same CLI also validates and manages MCP server registrations. Install it
from source when developing locally:

```bash
go install ./cmd/kavryctl
kavryctl version
```

Validate and register a manifest against the in-cluster Registry port-forward:

```bash
kavryctl validate cmd/kavryctl/examples/mcp-server.json

kavryctl register \
  --registry http://localhost:18081 \
  cmd/kavryctl/examples/mcp-server.json

kavryctl list --registry http://localhost:18081
kavryctl inspect --registry http://localhost:18081 example-mcp-server
kavryctl unregister --registry http://localhost:18081 example-mcp-server
```

For local-only workflows, omit `--registry`. State is stored in
`.kavrynt/registry.json` by default.

See the [kavryctl documentation](cmd/kavryctl/README.md) for all available
commands.

## Configuration

Override component settings through `kavryctl`:

```bash
kavryctl install \
  --set registry.image.tag=<version> \
  --set gateway.image.tag=<version> \
  --set operator.image.tag=<version>
```

Important values:

| Value | Default | Purpose |
| --- | --- | --- |
| `registry.image.repository` | `kavrynt/registry` | Registry container repository |
| `registry.image.tag` | `dev` | Registry image tag |
| `gateway.image.repository` | `kavrynt/gateway` | Gateway container repository |
| `gateway.image.tag` | `dev` | Gateway image tag |
| `gateway.config.registryURL` | In-cluster Registry service | Registry synchronization endpoint |
| `operator.image.repository` | `kavrynt/k8s-operator` | Operator container repository |
| `operator.image.tag` | `dev` | Operator image tag |
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

- [kavryctl runbook](cmd/kavryctl/docs/RUNBOOK.md)
- [Registry runbook](services/registry/docs/RUNBOOK.md)
- [Gateway runbook](services/gateway/docs/RUNBOOK.md)
- [Operator runbook](operator/docs/RUNBOOK.md)

### Uninstall

```bash
kavryctl uninstall
```

This keeps the `MCPServer` CRD and its custom resources. After confirming that
cluster-scoped data can be removed, purge the CRD and namespace as well:

```bash
kavryctl uninstall --purge --delete-namespace
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
- Audit, policy, approval, and usage services are not part of the public MVP.

Keep Registry and Gateway as internal `ClusterIP` services, restrict cluster
access, and do not expose this MVP directly to untrusted networks.

## Development

Prerequisites:

- Go 1.23+
- Docker
- Helm 3
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
make fmt-check
make docker-build
make helm-lint
make helm-template
make release-snapshot
```

The four modules are intentionally independently buildable:

```text
cmd/kavryctl
services/registry
services/gateway
operator
```

Repository-specific engineering and contribution rules are documented in
[`AGENTS.md`](AGENTS.md).

## Repository model

This repository is the main public Kavrynt product repository. It owns:

- the four core runtime components;
- Kubernetes APIs and controllers;
- Helm installation artifacts;
- release automation;
- executable samples and component runbooks.

Following the separation used by established Kubernetes projects such as
Istio, Kavrynt may later move community governance and the versioned product
website into dedicated public repositories. Until those repositories exist,
this README and the component documentation in this repository are the public
source of truth. The private `kavrynt-cloud` repository is not part of the
self-hosted runtime distribution.

## Roadmap

The next product milestones are:

1. durable Registry storage and upgrade-safe migrations;
2. Registry and Gateway authentication;
3. MCP Streamable HTTP and protocol-aware routing;
4. policy decisions and tool-level authorization;
5. audit and usage events;
6. upgrade and rollback commands in kavryctl;
7. upgrade, rollback, backup, and recovery workflows;
8. tested high-availability deployment profiles.

Hosted commercial management, cross-cluster inventory, enterprise identity,
and dedicated customer control planes belong to Kavrynt Cloud rather than this
public runtime repository.

## Project status

Kavrynt is under active development. APIs, chart values, and storage formats
may change before the first stable release.

Use [GitHub issues](https://github.com/kavrynt/kavrynt/issues) for reproducible
bugs and feature requests. Please include the Kavrynt version, Kubernetes
version, installation method, relevant manifests, and sanitized logs.

## License

Kavrynt is source-available under the [Elastic License 2.0](LICENSE).

You may use, copy, distribute, make available, and modify Kavrynt under the
license terms, but you may not provide Kavrynt to third parties as a hosted or
managed service that exposes a substantial set of Kavrynt's features.

Commercial managed-service rights and the hosted Kavrynt Cloud product are
licensed separately.
