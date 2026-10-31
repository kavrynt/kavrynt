# Kavrynt

[![QA](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml/badge.svg)](https://github.com/kavrynt/kavrynt/actions/workflows/qa.yml)
[![Release](https://img.shields.io/github/v/release/kavrynt/kavrynt?include_prereleases&sort=semver)](https://github.com/kavrynt/kavrynt/releases)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/kavrynt/kavrynt/badge)](https://scorecard.dev/viewer/?uri=github.com/kavrynt/kavrynt)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

> [!IMPORTANT]
> This is the source and release repository for the Kavrynt MCP Control Plane
> runtime: `kavryctl`, Gateway, Operator, the `MCPServer` CRD, and the Helm
> chart. See [ADR-0001](docs/ADR-0001-Runtime-Monorepo.md).

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
> loop works. MCP client authentication is on by default: an install names
> your authorization server or opts out explicitly. Tool authorization policy
> and audit events are available but off by default (the production profile
> turns audit on). Servers that need the user get the caller's token
> exchanged for their own audience. Approvals are not implemented yet. Run
> this version only in trusted development or evaluation clusters.

## TL;DR

A trial without an authorization server, with MCP routes open to anyone who
can reach the Gateway:

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 1.0.0-beta.3 \
  --namespace kavrynt-system \
  --create-namespace \
  --set gateway.auth.enabled=false

kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080
# console: http://localhost:18080/ui/  user: admin  password:
kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d
```

**Testing Kavrynt?** Follow the [developer testing runbook](docs/TESTING.md):
on any Kubernetes cluster, in about 30 minutes, it registers a sample MCP
server, calls its tools through the Gateway, and tests policy, audit, and the
console. Then tell us what worked in a
[tester feedback issue](https://github.com/kavrynt/kavrynt/issues/new?template=tester_feedback.yml).

## Contents

- [Why Kavrynt](#why-kavrynt)
- [Architecture](#architecture) and [Components](#components)
- [Install on Kubernetes](#install-on-kubernetes)
- [Try the complete product loop](#try-the-complete-product-loop)
- [Use kavryctl](#use-kavryctl)
- [Configuration](#configuration)
- [Operate Kavrynt](#operate-kavrynt)
- [Current security and durability boundaries](#current-security-and-durability-boundaries)
- [Contributing](#contributing)
- [Roadmap](#roadmap)
- [Community and maintainers](#community-and-maintainers)
- [License](#license)

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
| `operator` | Validate `MCPServer` resources, report `Accepted` and `Ready` conditions, and list each Ready server's tools into status |
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
supports HTTP endpoints only. A `stdio` server can run behind a Streamable
HTTP bridge and be registered with the bridge's endpoint:
[examples/stdio-bridge](examples/stdio-bridge/filesystem.yaml).

## Install on Kubernetes

### Prerequisites

- A conformant Kubernetes cluster.
- Helm 3.14 or newer (3.x or 4.x).
- `kubectl` access with permission to create CRDs, Deployments, Services,
  ServiceAccounts, ClusterRoles, and ClusterRoleBindings.

Kavrynt can run on managed or self-managed conformant Kubernetes clusters,
including GKE, EKS, AKS, OpenShift, and local Kind clusters.

### Install the control plane

```bash
export KAVRYNT_VERSION=1.0.0-beta.3

helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --create-namespace \
  --set gateway.auth.resourceURL=https://mcp.example.com \
  --set-json 'gateway.auth.issuers=[{"issuer":"https://login.example.com/realms/agents"}]'
```

MCP client authentication is on by default: every `/mcp/` call needs an
access token from a trusted authorization server, and the install fails
until `gateway.auth.resourceURL` (the Gateway's external https origin) and
`gateway.auth.issuers` are set. For a trial without an authorization server,
replace both with `--set gateway.auth.enabled=false`; MCP routes then accept
unauthenticated calls, and the Gateway and the console say so.

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
  --create-namespace \
  --set gateway.auth.enabled=false
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

### 5. Open the console

With the port-forward running, open <http://localhost:18080/ui/> and sign in
as `admin`. Like Argo CD, the chart generated the password into a Secret:

```bash
kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d
```

The read-only console opens on an **Overview** of what needs attention:
servers that are not routed, whose tool discovery failed, or whose calls are
failing, and the tools that fail most. **Servers** and **Tools** list every
`MCPServer` and every discovered tool; the **Playground** calls tools through
the Gateway exactly as an agent would; **Traffic** shows recent calls with
per-tool error rate and latency; the **Dashboard** charts requests, errors,
latency, and bytes over the last hour, 6 hours, or day for all servers, a
server, or a tool, with the console alerts marked. Point your own agent at
`http://localhost:18080/mcp/<namespace>.<name>` and watch its calls arrive.
The login protects the console only: MCP routes stay open unless MCP client
authentication (`gateway.auth`) is on.
Details: [Gateway console](docs/components/gateway/README.md#console).

### 6. Clean up the example

```bash
kubectl delete -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl delete service example-mcp-server
kubectl delete deployment example-mcp-server
```

## Run the demo

`make demo-up` creates a disposable Kind cluster with Kavrynt and two demo MCP
servers, and `make demo-walkthrough` presents the full product loop step by
step, including what Kavrynt does not do yet. See [docs/DEMO.md](docs/DEMO.md).

## Use kavryctl

kavryctl manages `MCPServer` resources through your kubeconfig. Helm
installs the control plane; `kavryctl upgrade` and `kavryctl rollback` run
Helm's upgrade and rollback with checks before and after (every routed
server must be routed again). Install it from source when developing
locally:

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

Commonly changed values:

| Value | Default | Purpose |
| --- | --- | --- |
| `global.watchNamespaces` | `[]` (all) | Namespaces the Gateway watches; a list switches Gateway RBAC to per-namespace Roles, and the Operator marks servers elsewhere `Ready=False` (`NamespaceNotWatched`). Replaces `gateway.config.watchNamespaces`. |
| `gateway.config.allowedOrigins` | `[]` | Browser origins accepted besides loopback, such as an Ingress that serves the console |
| `gateway.console.enabled` | `true` | Serve the read-only web console at `/ui/` |
| `gateway.console.auth.enabled` | `true` | Require the console admin login (password generated into a Secret) |
| `gateway.console.auth.existingSecret` | `""` | Your own Secret with `password` and `sessionKey`, for GitOps renders |
| `operator.config.toolDiscovery.enabled` | `true` | List the tools of `Ready` servers into `status.tools` |
| `operator.cloud.enabled` | `false` | Send inventory to Kavrynt Cloud |

Every value, with its type and default, is listed in the
[chart README](charts/kavrynt/README.md#values).

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

### Upgrade

`helm upgrade` with a newer `--version` upgrades the Gateway, the Operator,
and the `MCPServer` CRD together; `helm rollback` keeps the CRD and every
`MCPServer`. Upgrading from `0.0.5-beta.1` or earlier needs a one-time
command first, so Helm can take over the CRD; see
[Upgrading](charts/kavrynt/README.md#upgrading) in the chart README.

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

- Gateway authenticates MCP clients with OAuth 2.1 bearer tokens from your
  authorization server (on by default; `gateway.auth.enabled=false` opts
  out and leaves MCP routes open): each route accepts only tokens issued
  for it.
- Gateway never forwards `Authorization`, `Cookie`, or `Proxy-Authorization`
  to MCP servers, and drops upstream `Set-Cookie`, `Server`, and
  `X-Powered-By`. MCP servers that need
  their own credential get it from a Secret the platform team manages,
  pinned to the server's endpoint. Servers that need the user instead get
  the caller's token exchanged for one with their own audience (RFC 8693);
  the caller's token itself never reaches them.
- Gateway can decide every MCP call with a Rego tool authorization policy
  (`gateway.policy`, off by default), denying anything it does not allow.
- Gateway has read-only Kubernetes access to `MCPServer` resources and nothing
  else.
- Gateway and Operator never connect to cloud metadata, link-local, or
  loopback addresses, and the Gateway does not follow upstream redirects.
- Gateway rejects browser requests from origins other than loopback and
  `gateway.config.allowedOrigins`.
- Optional NetworkPolicies keep labelled MCP server pods reachable only from
  the Gateway and the Operator, and limit who can call the Gateway
  (`networkPolicy` values; needs a CNI that enforces NetworkPolicy).
- The console has a single `admin` login with a generated password, or
  sign-in through your identity provider for listed users and groups. It
  protects the console and the endpoints it reads, not MCP routes: anyone
  who can reach the Gateway can still call every routed tool.
- Gateway and Operator are single-replica by default; the tested
  high-availability profile is `examples/helm/values-production.yaml`.
- The Gateway can write one audit event per MCP request (`gateway.audit`):
  caller, tool, decision, and outcome, hash-chained per instance and checked
  with `kavryctl audit verify`, and can also deliver them to Kavrynt Cloud
  (`gateway.audit.cloud`). Approvals and usage services are not part of the
  current runtime beta.

Keep Gateway as an internal `ClusterIP` service, restrict cluster
access, and do not expose this MVP directly to untrusted networks.

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the
toolchain, repository layout, tests, sign-off, and pull request process.
User-visible changes are designed as specs first
([specs/README.md](specs/README.md)).

```bash
make help       # every target
make qa         # all quality and security gates
make build      # kavryctl, gateway, and operator binaries
make e2e-kind   # disposable Kind cluster, full product loop
```

Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).
Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
The release process is documented in [docs/RELEASE.md](docs/RELEASE.md).

## Roadmap

The full roadmap is in [ROADMAP.md](ROADMAP.md). The next product milestones
are:

1. approvals for sensitive tool calls
   ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md));
2. the console signed in through the authorization server;
3. audit delivery to Kavrynt Cloud end to end (the Gateway side is built) and usage events;
4. protocol-aware routing beyond the request checks;
5. recovery workflows (restoring into a new cluster).

Hosted commercial management, cross-cluster inventory, enterprise identity,
and dedicated customer control planes belong to Kavrynt Cloud rather than this
runtime repository.

## Community and maintainers

Kavrynt is under active development. APIs, chart values, and storage formats
may change before the first stable release.

The project is maintained by the people listed in
[MAINTAINERS.md](MAINTAINERS.md); roles and decision making are described in
[GOVERNANCE.md](GOVERNANCE.md).

Use [GitHub issues](https://github.com/kavrynt/kavrynt/issues) for reproducible
bugs and feature requests. Please include the Kavrynt version, Kubernetes
version, installation method, relevant manifests, and sanitized logs.

## License

Kavrynt is licensed under the [Apache License 2.0](LICENSE). Releases up to
and including `0.0.4-beta.1` were published under the Elastic License 2.0.
See [ADR-0004](docs/ADR-0004-Apache-2.0-License.md).
