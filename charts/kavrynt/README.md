# Kavrynt Helm Chart

![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square)
![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-informational?style=flat-square)
![Values: schema validated](https://img.shields.io/badge/Values-schema%20validated-informational?style=flat-square)

Kavrynt MCP Control Plane runtime chart.

## TL;DR

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 0.0.5-beta.1 \
  --namespace kavrynt-system \
  --create-namespace
```

## Introduction

This chart installs the Kavrynt Kubernetes runtime:

- Gateway (watches `MCPServer` resources read-only, routes MCP traffic, and
  serves the read-only web console)
- Kubernetes Operator (validates `MCPServer` resources, sets
  `Accepted`/`Ready`, lists their tools into status, optionally syncs them to
  Kavrynt Cloud)
- the `MCPServer` CRD (`crds/` of the operator subchart)

Component subcharts are vendored under `charts/`, so no
`helm dependency build` is needed. The chart version, the app version, and
both image tags are always the same release version.

## Prerequisites

- A conformant Kubernetes cluster.
- Helm 3.x or 4.x.
- Permission to create CRDs, ClusterRoles, and ClusterRoleBindings.

## Installing the chart

From the published OCI chart (TL;DR above), or from a checkout of this
repository:

```bash
helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace
```

Authenticate with `helm registry login ghcr.io` first when the chart package
is private.

Check the runtime:

```bash
kubectl get pods -n kavrynt-system
kubectl get svc -n kavrynt-system
```

Expected service: `kavrynt-gateway`.

## Upgrading

Helm installs CRDs only on first install and never upgrades them. Before
`helm upgrade`, apply the CRD from the target version's chart
(`charts/k8s-operator/crds/`) with `kubectl apply --server-side`.

## Uninstalling the chart

```bash
helm uninstall kavrynt -n kavrynt-system
```

Helm keeps the `MCPServer` CRD and its custom resources. Delete them
explicitly once you have confirmed that cluster-scoped data can be removed:

```bash
kubectl delete crd mcpservers.kavrynt.io
```

## Security notes

- The Gateway does not authenticate clients yet, and the console has no
  login. Keep the Gateway Service `ClusterIP` and disable the console with
  `gateway.console.enabled=false` where it is not needed.
- The Gateway has read-only access to `MCPServer` resources and no Secret
  access. Containers run as non-root with a read-only root filesystem.

See [SECURITY.md](../../SECURITY.md) for known limitations.

## Values

Values of the component subcharts are set under `gateway.` and `operator.`.

Every value is checked against the chart's JSON schemas
(`values.schema.json` in this chart and in each subchart). Unknown keys,
wrong types, origins that are not `http(s)://host[:port]`, durations Go
cannot parse (`30s`, `1m30s`), and invalid namespace names fail
`helm install`, `helm upgrade`, and `helm template` before anything is
applied. Check a values file with:

```bash
helm template kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 0.0.5-beta.1 --values my-values.yaml >/dev/null
```

`global` accepts additional keys for platform-injected globals.

### Global

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `global.watchNamespaces` | list | `[]` | Namespaces whose `MCPServer` resources the Gateway routes; empty means all. A list switches Gateway RBAC to per-namespace Roles, and the Operator reports servers elsewhere as not `Ready` (`NamespaceNotWatched`). |

### Gateway

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `gateway.replicaCount` | int | `1` | Gateway replicas. Recent traffic in the console is per replica. |
| `gateway.image.repository` | string | `kavrynt/gateway` | Gateway image repository |
| `gateway.image.tag` | string | release version | Gateway image tag |
| `gateway.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `gateway.imagePullSecrets` | list | `[]` | Image pull secrets |
| `gateway.serviceAccount.create` | bool | `true` | Create the Gateway service account |
| `gateway.serviceAccount.name` | string | `""` | Service account name; generated when empty |
| `gateway.rbac.create` | bool | `true` | Create read-only (`get`, `list`, `watch`) `MCPServer` RBAC |
| `gateway.service.type` | string | `ClusterIP` | Service type. Keep `ClusterIP`; the Gateway does not authenticate clients. |
| `gateway.service.port` | int | `8080` | Service port |
| `gateway.config.addr` | string | `":8080"` | Listen address |
| `gateway.config.requestTimeout` | string | `"30s"` | Upstream request timeout; event streams only until response headers |
| `gateway.config.stripRequestHeaders` | list | `[]` | Extra request headers never forwarded. `Authorization`, `Cookie`, and `Proxy-Authorization` are always removed. |
| `gateway.config.allowedOrigins` | list | `[]` | Browser origins accepted besides loopback, for example the Ingress URL that serves the console |
| `gateway.console.enabled` | bool | `true` | Serve the read-only web console at `/ui/` |
| `gateway.metrics.scrapeAnnotations` | bool | `true` | Add `prometheus.io/*` pod annotations |
| `gateway.metrics.serviceMonitor.enabled` | bool | `false` | Create a Prometheus Operator `ServiceMonitor` (requires its CRDs) |
| `gateway.metrics.serviceMonitor.interval` | string | `30s` | Scrape interval |
| `gateway.metrics.serviceMonitor.labels` | object | `{}` | Extra `ServiceMonitor` labels |
| `gateway.metrics.dashboard.enabled` | bool | `false` | Create the "Kavrynt MCP Traffic" Grafana dashboard ConfigMap |
| `gateway.metrics.dashboard.namespace` | string | `""` | Dashboard namespace; release namespace when empty |
| `gateway.metrics.dashboard.labels` | object | `{grafana_dashboard: "1"}` | Labels the Grafana sidecar selects |
| `gateway.podSecurityContext` | object | non-root `65532`, `RuntimeDefault` seccomp | Pod security context |
| `gateway.securityContext` | object | no privilege escalation, all capabilities dropped, read-only root filesystem | Container security context |
| `gateway.resources` | object | requests `25m`/`64Mi`, limits `250m`/`256Mi` | Container resources |
| `gateway.nodeSelector` | object | `{}` | Node selector |
| `gateway.tolerations` | list | `[]` | Tolerations |
| `gateway.affinity` | object | `{}` | Affinity |

### Operator

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `operator.replicaCount` | int | `1` | Operator replicas (leader election keeps one active) |
| `operator.image.repository` | string | `kavrynt/operator` | Operator image repository |
| `operator.image.tag` | string | release version | Operator image tag |
| `operator.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `operator.imagePullSecrets` | list | `[]` | Image pull secrets |
| `operator.serviceAccount.create` | bool | `true` | Create the Operator service account |
| `operator.serviceAccount.name` | string | `""` | Service account name; generated when empty |
| `operator.config.metricsBindAddress` | string | `":8080"` | Metrics listen address |
| `operator.config.healthProbeBindAddress` | string | `":8081"` | `/healthz` and `/readyz` listen address |
| `operator.config.leaderElect` | bool | `true` | Enable leader election |
| `operator.config.toolDiscovery.enabled` | bool | `true` | List the tools of `Ready` servers into `status.tools`. Needs network access to each endpoint; sends no credentials. |
| `operator.config.toolDiscovery.refreshInterval` | string | `5m` | Tool discovery refresh interval |
| `operator.cloud.enabled` | bool | `false` | Send `MCPServer` inventory and tools to Kavrynt Cloud |
| `operator.cloud.url` | string | `""` | Kavrynt Cloud base URL (https only) |
| `operator.cloud.tokenSecret.name` | string | `""` | Secret in the release namespace holding the cluster token |
| `operator.cloud.tokenSecret.key` | string | `token` | Key of the token in that Secret |
| `operator.cloud.syncInterval` | string | `60s` | Inventory sync interval |
| `operator.podSecurityContext` | object | non-root `65532`, `RuntimeDefault` seccomp | Pod security context |
| `operator.securityContext` | object | no privilege escalation, all capabilities dropped, read-only root filesystem | Container security context |
| `operator.resources` | object | requests `25m`/`64Mi`, limits `250m`/`256Mi` | Container resources |
| `operator.nodeSelector` | object | `{}` | Node selector |
| `operator.tolerations` | list | `[]` | Tolerations |
| `operator.affinity` | object | `{}` | Affinity |

Component flags behind these values are documented in
[docs/components](../../docs/components/).
