# Kavrynt Helm Chart

![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square)
![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-informational?style=flat-square)
![Values: schema validated](https://img.shields.io/badge/Values-schema%20validated-informational?style=flat-square)

Kavrynt MCP Control Plane runtime chart.

## TL;DR

```bash
# With your authorization server (MCP client authentication is on by default):
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 1.0.0-beta.3 \
  --namespace kavrynt-system \
  --create-namespace \
  --set gateway.auth.resourceURL=https://mcp.example.com \
  --set-json 'gateway.auth.issuers=[{"issuer":"https://login.example.com/realms/agents"}]'

# A trial without one: MCP routes accept unauthenticated calls.
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version 1.0.0-beta.3 \
  --namespace kavrynt-system \
  --create-namespace \
  --set gateway.auth.enabled=false
```

Without either, the install fails and names both. See
[Authenticating MCP clients](#authenticating-mcp-clients).

## Introduction

This chart installs the Kavrynt Kubernetes runtime:

- Gateway (watches `MCPServer` resources read-only, routes MCP traffic, and
  serves the read-only web console)
- Kubernetes Operator (validates `MCPServer` resources, sets
  `Accepted`/`Ready`, lists their tools into status, optionally syncs them to
  Kavrynt Cloud)
- the `MCPServer` CRD, installed and upgraded with the chart and kept on
  uninstall and rollback

Component subcharts are vendored under `charts/`, so no
`helm dependency build` is needed. The chart version, the app version, and
both image tags are always the same release version.

## Prerequisites

- A conformant Kubernetes cluster.
- Helm 3.14 or newer (3.x or 4.x).
- Permission to create CRDs, ClusterRoles, and ClusterRoleBindings.

## Installing the chart

From the published OCI chart (TL;DR above), or from a checkout of this
repository:

```bash
helm upgrade --install kavrynt charts/kavrynt \
  --namespace kavrynt-system \
  --create-namespace \
  --set gateway.auth.enabled=false
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

`helm upgrade` upgrades the Gateway, the Operator, and the `MCPServer` CRD
together:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values
```

`--reset-then-reuse-values` keeps your values and takes the new chart's
defaults. Do not use `--reuse-values` across versions: it keeps the previous
chart's defaults, including its image tags, so the old images keep running.

### Once, when upgrading from 0.0.5-beta.1 or earlier

Those releases installed the CRD outside Helm's control (from the chart's
`crds/` directory), so Helm refuses to take it over and the upgrade fails
before changing anything, naming `mcpservers.kavrynt.io`. Let Helm adopt the
CRD first; use your release name and namespace:

```bash
kubectl label crd mcpservers.kavrynt.io app.kubernetes.io/managed-by=Helm --overwrite
kubectl annotate crd mcpservers.kavrynt.io \
  meta.helm.sh/release-name=kavrynt meta.helm.sh/release-namespace=kavrynt-system --overwrite
```

Then run `helm upgrade` as above. Existing `MCPServer` resources are kept.

### Once, when upgrading to 1.0.0-beta.1

From `1.0.0-beta.1`, MCP client authentication is on by default. An upgrade
of a release that never set `gateway.auth` fails before changing anything,
with a message naming both ways forward. Add your authorization server:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set gateway.auth.resourceURL=https://mcp.example.com \
  --set-json 'gateway.auth.issuers=[{"issuer":"https://login.example.com/realms/agents"}]'
```

or keep MCP routes open, as they were, with
`--set gateway.auth.enabled=false`.

### With kavryctl

`kavryctl upgrade --version <version>` runs the same upgrade with checks:
it stops before changing anything when the release or the version is
missing or the CRD needs adoption (`--adopt-crd` adopts it), reuses the
release's values, and reports every server that was routed before and is
not now. `kavryctl rollback [--revision N]` does the same for `helm
rollback`. See [kavryctl: Upgrade and rollback](../../docs/components/kavryctl/README.md#upgrade-and-rollback).

### Rolling back

`helm rollback` restores the previous Gateway and Operator. The CRD and every
`MCPServer` stay in place (the CRD carries `helm.sh/resource-policy: keep`);
the CRD keeps the newer schema, which older releases accept because schema
changes are additive within `v1alpha1`.

### Managing the CRD yourself

Set `operator.crds.install=false` when CRDs are applied outside this release,
for example by a cluster administrator when the installer may not update
CRDs. Apply `charts/k8s-operator/files/kavrynt.io_mcpservers.yaml` from the
target version's chart with `kubectl apply --server-side` before upgrading.

## Uninstalling the chart

```bash
helm uninstall kavrynt -n kavrynt-system
```

Helm keeps the `MCPServer` CRD and its custom resources (keep policy). Delete
them explicitly once you have confirmed that cluster-scoped data can be
removed:

```bash
kubectl delete crd mcpservers.kavrynt.io
```

## Monitoring and alerts

Both components expose Prometheus metrics: the Gateway on its Service
(`gateway.metrics.*`), the Operator on `<release>-operator-metrics`
(`operator.metrics.*`). With the Prometheus Operator:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set gateway.metrics.serviceMonitor.enabled=true \
  --set operator.metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true
```

`metrics.prometheusRule` installs these alerts, each with a runbook in
[docs/runbooks](../../docs/runbooks/README.md) linked from its `runbook_url`:

| Alert | Severity | Fires when |
| --- | --- | --- |
| `KavryntGatewayDown` | critical | No Gateway target up for 5 minutes |
| `KavryntGatewayRouteSyncFailing` | critical | A replica's route syncs failed for 10 minutes without a success |
| `KavryntMCPUpstreamErrorsHigh` | warning | Over `errorRatio` (5%) of a route's calls fail upstream for 10 minutes |
| `KavryntMCPLatencyHigh` | warning | A route's p95 latency is over `latencyP95Seconds` (5s) for 15 minutes |
| `KavryntMCPToolErrorsHigh` | warning | Over `toolErrorRatio` (20%) of a tool's calls end in `tool_error` or `rpc_error` for 10 minutes |
| `KavryntOperatorDown` | critical | No Operator target up for 5 minutes |
| `KavryntMCPServerNotReady` | warning | An `MCPServer` is not `Ready` for `notReadyFor` (10m) |
| `KavryntMCPToolDiscoveryFailing` | warning | The Operator cannot list a `Ready` server's tools for `discoveryFailingFor` (15m) |
| `KavryntCloudSyncStale` | warning | Kavrynt Cloud accepted no snapshot for `cloudSyncStaleSeconds` (600); only with the Cloud connector |
| `KavryntAuditDeliveryStale` | warning | A Gateway replica has audit events waiting and Kavrynt Cloud accepted none for `auditDeliveryStaleSeconds` (600); only with `gateway.audit.cloud.enabled` |

The console's Alerts page shows the MCP server conditions of these alerts
without Prometheus, evaluated by the Gateway with the same
`metrics.prometheusRule` thresholds (written to the ConfigMap
`<release>-console-alerts` even while the rules are off); see
[console alerts](../../docs/components/gateway/README.md#console-alerts).

Set `metrics.prometheusRule.labels` to match your Prometheus `ruleSelector`,
`alertLabels` to route alerts in Alertmanager, and `jobs` when Prometheus
discovers the targets without the chart's ServiceMonitors. The Operator's
`MCPServer` metrics carry the server's `namespace` and `name`, so scrape them
with `honor_labels: true` (the chart's ServiceMonitor does); otherwise
Prometheus replaces `namespace` with the Operator's. Every alert carries
`kavrynt_release` (the release name). The rules are tested with `promtool`
(`make alerts-test`).

### Notifications

`metrics.notifications` installs an `AlertmanagerConfig` that sends this
release's alerts to Slack, email, Microsoft Teams, or a webhook (for example a
PagerDuty bridge). Each message names the alert, its severity, whether it is
firing or resolved, the server (and tool), what is wrong, and the runbook.
URLs and passwords stay in Secrets in the release namespace; values only name
them.

```bash
kubectl -n kavrynt-system create secret generic kavrynt-alerts-slack \
  --from-literal=url=https://hooks.slack.com/services/...
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --values examples/helm/values-notifications-slack.yaml
```

[`values-notifications-slack.yaml`](../../examples/helm/values-notifications-slack.yaml)
and [`values-notifications-email.yaml`](../../examples/helm/values-notifications-email.yaml)
are tested examples. A receiver is on once its Secret (email: `to`) is set,
and takes the severities in its `severities` list, so critical alerts can go
to a pager webhook and warnings to Slack. Alerts are grouped by alert and
server and repeated every `repeatInterval` (4h) while they fire.

The Alertmanager must select the config and must not scope it to its own
namespace, because alerts about an `MCPServer` carry that server's namespace.
With kube-prometheus-stack:

```yaml
alertmanager:
  alertmanagerSpec:
    alertmanagerConfigSelector:
      matchLabels:
        alertmanagerConfig: kavrynt   # metrics.notifications.labels
    alertmanagerConfigMatcherStrategy:
      type: None
```

Teams needs Prometheus Operator v0.82 or later (`msteamsv2Configs`, a Teams
workflow webhook). The configuration is checked with `amtool` in CI
(`make alerts-test`).

### Dashboards

`gateway.metrics.dashboard.enabled` installs two Grafana dashboards as
ConfigMaps for the Grafana sidecar (`gateway.metrics.dashboard.labels`,
`grafana_dashboard: "1"` by default): "Kavrynt MCP Traffic" for every server,
and "Kavrynt MCP Server" for one server (readiness, tool discovery, traffic,
errors, latency per tool, failing tools, and bytes). The metrics and labels
you can use are listed in the
[Gateway](../../docs/components/gateway/README.md#metrics-reference) and
[Operator](../../docs/components/operator/README.md#metrics-reference)
metrics references.

To release your own dashboards with Kavrynt, build them in Grafana, then
**Share > Export** (leave "Export for sharing externally" off, so the
datasource stays a variable) and save the JSON. Add each file under
`gateway.metrics.dashboard.extra`:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set gateway.metrics.dashboard.enabled=true \
  --set-file 'gateway.metrics.dashboard.extra.team-mcp\.json=team-mcp.json'
```

or in a values file, as JSON text or YAML:

```yaml
gateway:
  metrics:
    dashboard:
      enabled: true
      extra:
        team-mcp.json: |
          {"title": "Team MCP", "uid": "team-mcp", "panels": []}
```

Each entry becomes a ConfigMap `<release>-gateway-dashboard-<name>` with the
same labels, so `helm upgrade` updates it and `helm uninstall` removes it.
File names are lower case, end in `.json`, and must be unique; a value that
is not JSON fails the install. A ConfigMap holds at most 1 MiB.

## High availability

[`examples/helm/values-production.yaml`](../../examples/helm/values-production.yaml)
is the tested production profile: three Gateway replicas and two Operator
replicas, each with a PodDisruptionBudget, and the Gateway spread across
nodes and zones.

```bash
helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version <version> --namespace kavrynt-system --create-namespace \
  --values values-production.yaml
```

The end-to-end test installs it, restarts every Gateway replica while a
client calls tools continuously (no call may fail), and deletes the Operator
leader (the standby must reconcile new servers). Recent traffic in the
console, the console's dashboards and traffic alerts, and the console login
limiter are per Gateway replica; use Prometheus for cluster-wide traffic.

## Backing up

`MCPServer` resources are ordinary Kubernetes objects: the cluster's backup
(etcd snapshots, Velero) covers them. To export them by hand:

```bash
kubectl get mcpservers.kavrynt.io -A -o yaml > mcpservers-backup.yaml
```

Restore with `kubectl apply -f mcpservers-backup.yaml` after installing the
chart. Status (conditions, tools, reported version) is recomputed by the
Operator; it does not need to be restored.

## Security notes

- MCP client authentication is on by default. With
  `gateway.auth.enabled=false`, anyone who can reach the Gateway can call
  every routed tool through `/mcp/`: keep the Gateway Service `ClusterIP`
  while it is off (see "Authenticating MCP clients").
- The console has a single `admin` login. The chart generates its password
  into the Secret `kavrynt-gateway-console-admin` (kept on uninstall); read it
  with `kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d`. The login protects the console and
  `/v1/servers`, `/v1/traffic`, `/v1/alerts`, not MCP routes. With `helm template` or GitOps,
  set `gateway.console.auth.existingSecret`, because a render without cluster
  access generates a new password each time.
- The Gateway has read-only access to `MCPServer` resources and, for
  session affinity, EndpointSlices, and no Secret access. Containers run as non-root with a read-only root filesystem.

See [SECURITY.md](../../SECURITY.md) for known limitations.

## Credentials for MCP servers

MCP servers that need their own API key declare `spec.credential` in their
`MCPServer`. Store the values in one Secret in the release namespace, two keys
per server, and name it in the chart:

```bash
kubectl -n kavrynt-system create secret generic kavrynt-upstream-credentials \
  --from-literal=team-a.search="$SEARCH_TOKEN" \
  --from-literal=team-a.search.origin=http://search.team-a.svc.cluster.local:8080
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set global.upstreamCredentials.secretName=kavrynt-upstream-credentials
```

The `.origin` key pins the credential to the server's endpoint. Details:
[Gateway: Upstream credentials](../../docs/components/gateway/README.md#upstream-credentials).

## Audit events

`gateway.audit.enabled=true` (on in the production profile) makes the Gateway
write one JSON line per MCP request to standard output: who called which tool
on which server, the decision, and the outcome, never arguments or tokens.
Collect them with your log pipeline (filter on `"type":"kavrynt.audit.v1"`)
and check their hash chains:

```bash
kubectl -n kavrynt-system logs -l app.kubernetes.io/name=gateway --tail=-1 --prefix=false > gateway.log
kavryctl audit verify gateway.log
```

To also deliver the events to Kavrynt Cloud, with the same cluster token
Secret as the Operator's connector:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set gateway.audit.cloud.enabled=true \
  --set gateway.audit.cloud.url=https://<your Kavrynt Cloud URL> \
  --set gateway.audit.cloud.tokenSecret.name=kavrynt-cloud-token
```

Events then leave the cluster, including each caller's subject and client
ID. Standard output keeps every event; events that wait for Cloud are
buffered (`gateway.audit.cloud.bufferSize`, 100,000) and the alert
`KavryntAuditDeliveryStale` fires when Cloud accepts none for 10 minutes.
Kavrynt Cloud must serve the ingestion endpoint `POST /v1/sync/audit-events`.

Details: [Gateway: Audit events](../../docs/components/gateway/README.md#audit-events).

## Tool authorization policy

The Gateway can decide every MCP call with a Rego policy (who may call which
tool on which server). Put the `.rego` files in a ConfigMap and enable it:

```bash
kubectl -n kavrynt-system create configmap kavrynt-policy --from-file=examples/policy/client-allowlist.rego
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set gateway.policy.enabled=true --set gateway.policy.configMap=kavrynt-policy
```

Anything the policy does not allow is denied. Use it with MCP client
authentication so decisions can name the caller. Add
`--set gateway.policy.filterToolsList=true` so each caller's `tools/list`
shows only the tools it may call. A change to the ConfigMap takes one to two
minutes to apply (the kubelet's mount update, then the Gateway's 10-second
check), and until then the previous policy decides; restart the Gateway to
apply one at once. Details:
[Gateway: Tool authorization policy](../../docs/components/gateway/README.md#tool-authorization-policy).

## Authenticating MCP clients

The Gateway requires an OAuth 2.1 bearer token from your authorization
server on every MCP call; it is on by default. Each route accepts only
tokens whose audience is its resource URI,
`<resourceURL>/mcp/<namespace>.<name>`.

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set gateway.auth.enabled=true \
  --set gateway.auth.resourceURL=https://mcp.example.com \
  --set-json 'gateway.auth.issuers=[{"issuer":"https://login.example.com/realms/agents"}]' \
  --set 'gateway.auth.requiredScopes={mcp:tools}'
```

`resourceURL` is the origin agents use to reach the Gateway (no path).
Issuers must be https; an in-cluster authorization server on plain http
needs `allowHTTP: true`. The chart refuses to render authentication without
a resource URL or an issuer; `gateway.auth.enabled=false` runs without
authentication instead, and the Gateway logs a warning at start and the
console shows that MCP routes are open. Agents without a valid token get `401` with a
`resource_metadata` URL that tells them where to get one. Token rules and
answers: [Gateway: MCP client authentication](../../docs/components/gateway/README.md#mcp-client-authentication).
A walkthrough with Keycloak, local testing with a plain-http port-forward,
and agents that call several servers:
[Setting up MCP client authentication](../../docs/AUTH-SETUP.md).

### Token exchange for servers that need the user

An MCP server that checks who the user is declares
`spec.tokenExchange.audience`; the Gateway exchanges each caller's token for
one with that audience at the caller's authorization server (RFC 8693). Give
the Gateway an OAuth client there, store its secret, and name both:

```bash
kubectl -n kavrynt-system create secret generic kavrynt-token-exchange \
  --from-literal=client-secret='<the Gateway client secret>'
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
  --namespace kavrynt-system --reset-then-reuse-values \
  --set-json 'gateway.auth.issuers=[{"issuer":"https://login.example.com/realms/agents","tokenExchange":{"clientID":"kavrynt-gateway","clientSecretKey":"client-secret"}}]' \
  --set gateway.auth.tokenExchangeSecret=kavrynt-token-exchange
```

The authorization server must allow that client to exchange tokens for the
server's audience. Details:
[Gateway: Token exchange](../../docs/components/gateway/README.md#token-exchange)
and [`examples/kavryctl/mcp-server-token-exchange.yaml`](../../examples/kavryctl/mcp-server-token-exchange.yaml).

## Keeping traffic on the Gateway

Everything the Gateway enforces is skipped by a client that calls an MCP
server's Service directly. Two opt-in NetworkPolicies close those paths; they
need a CNI that enforces NetworkPolicy (Calico, Cilium, and kindnet do) and
have no effect elsewhere.

1. Label the pods of each MCP server to protect:

   ```yaml
   spec:
     template:
       metadata:
         labels:
           kavrynt.io/gateway-only: "true"
   ```

2. Enable isolation for their namespaces (the namespaces must exist, and
   must be in `global.watchNamespaces` when that is set):

   ```bash
   helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version <version> \
     --namespace kavrynt-system --reset-then-reuse-values \
     --set networkPolicy.mcpServers.enabled=true \
     --set 'networkPolicy.mcpServers.namespaces={team-a,team-b}'
   ```

   Labelled pods then accept connections only from this release's Gateway
   and Operator (tool discovery); unlabelled pods are not affected.

3. Optionally restrict who may call the Gateway: `networkPolicy.gateway`
   admits its own namespace, namespaces matching `allowedNamespaceLabels`,
   and `allowedCIDRs`, on the Gateway port only. Include your agents, your
   Ingress controller, and Prometheus. Egress is not restricted.

The end-to-end test runs with both policies on and checks that a direct call
to a labelled MCP server fails, even from its own namespace, while the same
call through the Gateway succeeds.

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
  --version 1.0.0-beta.3 --values my-values.yaml >/dev/null
```

`global` accepts additional keys for platform-injected globals.

### Global

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `global.watchNamespaces` | list | `[]` | Namespaces whose `MCPServer` resources the Gateway routes; empty means all. A list switches Gateway RBAC to per-namespace Roles, and the Operator reports servers elsewhere as not `Ready` (`NamespaceNotWatched`). |
| `global.endpoints.crossNamespace` | list | `[]` | `from`/`to` namespace pairs whose MCPServers in `from` may use endpoints in `to`; otherwise an endpoint in another namespace is not `Ready` (`EndpointNotAllowed`) |
| `global.endpoints.clusterCIDRs` | list | `[]` | The cluster's Service and Pod CIDRs; IP endpoints inside them are not `Ready` |
| `global.tokenExchangeAudiences` | object | `{}` | Namespace (or `"*"`) to the `spec.tokenExchange.audience` values its MCPServers may use; others are not `Ready` (`AudienceNotAllowed`) and get no token exchange. Empty allows none |
| `global.upstreamCredentials.secretName` | string | `""` | Secret in the release namespace with the credentials of MCP servers that declare `spec.credential`; mounted (optional) into the Gateway and the Operator, which get no Secret RBAC |

### Alerts

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `metrics.prometheusRule.enabled` | bool | `false` | Create the `PrometheusRule` with the Kavrynt alerts (requires the Prometheus Operator CRDs) |
| `metrics.prometheusRule.labels` | object | `{}` | Labels on the `PrometheusRule`, for Prometheus' `ruleSelector` |
| `metrics.prometheusRule.alertLabels` | object | `{}` | Labels added to every alert; `severity` and `component` cannot be overridden |
| `metrics.prometheusRule.jobs.gateway` | string | Gateway Service name | Prometheus job of the Gateway targets |
| `metrics.prometheusRule.jobs.operator` | string | `<release>-operator-metrics` | Prometheus job of the Operator targets |
| `metrics.prometheusRule.errorRatio` | number | `0.05` | Upstream failure share that raises `KavryntMCPUpstreamErrorsHigh` |
| `metrics.prometheusRule.minRequestsPerSecond` | number | `0.05` | Routes with less traffic never raise it |
| `metrics.prometheusRule.latencyP95Seconds` | number | `5` | p95 latency that raises `KavryntMCPLatencyHigh` |
| `metrics.prometheusRule.notReadyFor` | string | `10m` | How long an `MCPServer` may be not `Ready` |
| `metrics.prometheusRule.toolErrorRatio` | number | `0.2` | Share of a tool's calls ending in `tool_error` or `rpc_error` that raises `KavryntMCPToolErrorsHigh` |
| `metrics.prometheusRule.minToolRequestsPerSecond` | number | `0.02` | Tools with fewer `tools/call` requests never raise it |
| `metrics.prometheusRule.discoveryFailingFor` | string | `15m` | How long tool discovery of a `Ready` `MCPServer` may fail |
| `metrics.prometheusRule.cloudSyncStaleSeconds` | int | `600` | Age of the last accepted Cloud snapshot that raises `KavryntCloudSyncStale` |
| `metrics.prometheusRule.auditDeliveryStaleSeconds` | int | `600` | Age of the last accepted audit batch, with events waiting, that raises `KavryntAuditDeliveryStale` |
| `metrics.prometheusRule.runbookBaseURL` | string | this repository at the release tag | Base of every `runbook_url` |
| `metrics.notifications.enabled` | bool | `false` | Create the `AlertmanagerConfig` (requires `metrics.prometheusRule.enabled` and a receiver) |
| `metrics.notifications.labels` | object | `{}` | Labels on the `AlertmanagerConfig`, for `alertmanagerConfigSelector` |
| `metrics.notifications.groupWait` / `groupInterval` / `repeatInterval` | string | `30s` / `5m` / `4h` | Alertmanager grouping and repeat timing |
| `metrics.notifications.slack.webhookSecret` | object | `{name: "", key: url}` | Secret key with the Slack webhook URL; a name turns Slack on |
| `metrics.notifications.slack.channel` | string | `""` | Channel; empty uses the webhook's |
| `metrics.notifications.email.to` | list | `[]` | Recipients; any turn email on |
| `metrics.notifications.email.from` / `smarthost` / `authUsername` | string | `""` | SMTP settings; empty uses the Alertmanager's global ones |
| `metrics.notifications.email.authPasswordSecret` | object | `{name: "", key: password}` | Secret key with the SMTP password |
| `metrics.notifications.email.requireTLS` | bool | `true` | Require STARTTLS |
| `metrics.notifications.msteams.webhookSecret` | object | `{name: "", key: url}` | Secret key with the Teams workflow webhook URL |
| `metrics.notifications.webhook.urlSecret` | object | `{name: "", key: url}` | Secret key with a webhook URL (Alertmanager's JSON payload) |
| `metrics.notifications.<receiver>.severities` | list | `[critical, warning]` | Severities the receiver gets |

### Network policies

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `networkPolicy.mcpServers.enabled` | bool | `false` | In each listed namespace, pods labelled `kavrynt.io/gateway-only: "true"` accept traffic only from the Gateway and the Operator |
| `networkPolicy.mcpServers.namespaces` | list | `[]` | Namespaces to isolate; required when enabled, existing, and within `global.watchNamespaces` when that is set |
| `networkPolicy.gateway.enabled` | bool | `false` | Limit ingress to the Gateway port to the release namespace and the entries below |
| `networkPolicy.gateway.allowedNamespaceLabels` | list | `[]` | Label sets of namespaces allowed to call the Gateway, for example `[{kubernetes.io/metadata.name: agents}]` |
| `networkPolicy.gateway.allowedCIDRs` | list | `[]` | CIDRs allowed to call the Gateway |

### Gateway

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `gateway.replicaCount` | int | `1` | Gateway replicas. Recent traffic in the console is per replica. Ignored when autoscaling is on |
| `gateway.autoscaling.enabled` | bool | `false` | HorizontalPodAutoscaler on CPU (needs metrics-server) |
| `gateway.autoscaling.minReplicas` | int | `2` | Minimum replicas (at least 2) |
| `gateway.autoscaling.maxReplicas` | int | `5` | Maximum replicas |
| `gateway.autoscaling.targetCPUUtilizationPercentage` | int | `70` | Target average CPU utilisation |
| `gateway.podDisruptionBudget.enabled` | bool | `true` | PodDisruptionBudget when more than one replica runs |
| `gateway.podDisruptionBudget.maxUnavailable` | int or string | `1` | Replicas a voluntary disruption may take at once (`1` or a percentage) |
| `gateway.topologySpreadConstraints` | list | `[]` | Pod spread; empty uses a soft spread across nodes and zones when more than one replica runs |
| `gateway.image.repository` | string | `kavrynt/gateway` | Gateway image repository |
| `gateway.image.tag` | string | release version | Gateway image tag |
| `gateway.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `gateway.imagePullSecrets` | list | `[]` | Image pull secrets |
| `gateway.serviceAccount.create` | bool | `true` | Create the Gateway service account |
| `gateway.serviceAccount.name` | string | `""` | Service account name; generated when empty |
| `gateway.rbac.create` | bool | `true` | Create read-only (`get`, `list`, `watch`) RBAC on `MCPServer` resources and EndpointSlices (for session affinity) |
| `gateway.service.type` | string | `ClusterIP` | Service type. Keep `ClusterIP` unless MCP client authentication is on. |
| `gateway.service.port` | int | `8080` | Service port |
| `gateway.config.addr` | string | `":8080"` | Listen address |
| `gateway.config.requestTimeout` | string | `"30s"` | Upstream request timeout; event streams only until response headers |
| `gateway.config.stripRequestHeaders` | list | `[]` | Extra request headers never forwarded. `Authorization`, `Cookie`, and `Proxy-Authorization` are always removed. |
| `gateway.config.allowedOrigins` | list | `[]` | Browser origins accepted besides loopback, for example the Ingress URL that serves the console |
| `gateway.config.shutdownDelay` | string | `5s` | On shutdown, keep serving this long after `/readyz` fails (whole seconds) |
| `gateway.config.shutdownTimeout` | string | `10s` | Then wait this long for in-flight requests (whole seconds). The termination grace period is both plus 5 seconds |
| `gateway.auth.enabled` | bool | `true` | Require a bearer access token for the route on every `/mcp/` request (MCP client authentication); `false` leaves MCP routes open |
| `gateway.auth.resourceURL` | string | `""` | The Gateway's external https origin; required when enabled |
| `gateway.auth.issuers` | list | `[]` | Trusted authorization servers: `issuer` (equals the tokens' `iss`), optional `jwksURI`, `allowHTTP`; at least one when enabled |
| `gateway.auth.requiredScopes` | list | `[]` | Scopes every token must carry |
| `gateway.auth.gatewayAudience.enabled` | bool | `false` | Let the listed routes also accept a token whose audience is `resourceURL` (SPEC-0031); needs `gateway.auth.enabled` |
| `gateway.auth.gatewayAudience.routes` | list | `[]` | Routes one such token reaches: `<namespace>.<name>` or `<namespace>.*`; required when enabled, `*` refused |
| `gateway.audit.enabled` | bool | `false` | Write one audit event per MCP request to the Gateway's standard output |
| `gateway.audit.queueSize` | int | `10000` | Audit events that may wait to be written |
| `gateway.audit.onOverflow` | string | `reject` | With a full queue: `reject` new MCP requests or `drop` their events |
| `gateway.policy.enabled` | bool | `false` | Decide every MCP request with the tool authorization policy |
| `gateway.policy.configMap` | string | `""` | ConfigMap with the `.rego` files; required when enabled |
| `gateway.console.enabled` | bool | `true` | Serve the read-only web console at `/ui/` |
| `gateway.console.auth.enabled` | bool | `true` | Require the console admin login for the console, `/v1/servers`, and `/v1/traffic`. Does not protect MCP routes |
| `gateway.console.auth.existingSecret` | string | `""` | Secret with `password` and `sessionKey` keys to use instead of the generated `<release>-gateway-console-admin` |
| `gateway.console.auth.sessionTTL` | string | `12h` | Console session lifetime |
| `gateway.console.auth.trustedProxies` | list | `[]` | CIDRs of the proxies in front of the Gateway; failed console logins from them count against the client their `X-Forwarded-For` names, not the proxy |
| `gateway.console.auth.oidc.enabled` | bool | `false` | Sign in to the console through an OpenID Connect provider (SPEC-0032); needs `gateway.console.auth.enabled` |
| `gateway.console.auth.oidc.issuer` / `allowHTTP` | string / bool | `""` / `false` | The provider's issuer; `allowHTTP` only for an in-cluster provider (the browser's authorization endpoint must still be https) |
| `gateway.console.auth.oidc.clientID` / `clientSecret.name` / `clientSecret.key` | string | `""` / `""` / `client-secret` | The confidential client, and the Secret holding its secret (mounted, never a flag) |
| `gateway.console.auth.oidc.redirectURL` | string | `""` | `https://<gateway host>/v1/session/oidc/callback` |
| `gateway.console.auth.oidc.allowedSubjects` / `allowedGroups` | list | `[]` | Who may sign in; at least one entry in either is required |
| `gateway.console.auth.oidc.groupsClaim` / `displayClaim` / `scopes` / `providerName` | string / string / list / string | `groups` / `preferred_username` / `[]` / `""` | Claims read, scopes requested besides `openid`, and the button's provider name |
| `gateway.console.auth.oidc.keepAdminLogin` | bool | `false` | Keep the admin password as a break-glass login (warned about at start) |
| `gateway.console.history.maxTools` | int | `500` | Tools the console dashboards chart on their own, across routes (1 to 10000); further tools count as their route's `other` tool. Each replica keeps 24 hours in memory: about 29 KiB per route and 23 KiB per tool |
| `gateway.console.grafanaURL` | string | `""` | Grafana base URL (http or https) the console dashboards link for cluster-wide history; empty names the Grafana dashboards instead |
| `gateway.metrics.scrapeAnnotations` | bool | `true` | Add `prometheus.io/*` pod annotations |
| `gateway.metrics.serviceMonitor.enabled` | bool | `false` | Create a Prometheus Operator `ServiceMonitor` (requires its CRDs) |
| `gateway.metrics.serviceMonitor.interval` | string | `30s` | Scrape interval |
| `gateway.metrics.serviceMonitor.labels` | object | `{}` | Extra `ServiceMonitor` labels |
| `gateway.metrics.dashboard.enabled` | bool | `false` | Create the "Kavrynt MCP Traffic" and "Kavrynt MCP Server" Grafana dashboard ConfigMap |
| `gateway.metrics.dashboard.namespace` | string | `""` | Dashboard namespace; release namespace when empty |
| `gateway.metrics.dashboard.labels` | object | `{grafana_dashboard: "1"}` | Labels the Grafana sidecar selects |
| `gateway.metrics.dashboard.extra` | object | `{}` | Your dashboards: `<name>.json` to dashboard JSON (string or object), one ConfigMap each |
| `gateway.podSecurityContext` | object | non-root `65532`, `RuntimeDefault` seccomp | Pod security context |
| `gateway.securityContext` | object | no privilege escalation, all capabilities dropped, read-only root filesystem | Container security context |
| `gateway.resources` | object | requests `25m`/`64Mi`, limits `250m`/`256Mi` | Container resources |
| `gateway.nodeSelector` | object | `{}` | Node selector |
| `gateway.tolerations` | list | `[]` | Tolerations |
| `gateway.affinity` | object | `{}` | Affinity |

### Operator

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `operator.replicaCount` | int | `1` | Operator replicas (leader election keeps one active; more than one requires `config.leaderElect`) |
| `operator.podDisruptionBudget.enabled` | bool | `true` | PodDisruptionBudget when more than one replica runs |
| `operator.podDisruptionBudget.maxUnavailable` | int or string | `1` | Replicas a voluntary disruption may take at once |
| `operator.crds.install` | bool | `true` | Install and upgrade the `MCPServer` CRD with the chart |
| `operator.crds.keep` | bool | `true` | Keep the CRD, and with it every `MCPServer`, on uninstall and rollback |
| `operator.image.repository` | string | `kavrynt/operator` | Operator image repository |
| `operator.image.tag` | string | release version | Operator image tag |
| `operator.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `operator.imagePullSecrets` | list | `[]` | Image pull secrets |
| `operator.serviceAccount.create` | bool | `true` | Create the Operator service account |
| `operator.serviceAccount.name` | string | `""` | Service account name; generated when empty |
| `operator.config.metricsBindAddress` | string | `":8080"` | Metrics listen address |
| `operator.config.healthProbeBindAddress` | string | `":8081"` | `/healthz` and `/readyz` listen address |
| `operator.config.leaderElect` | bool | `true` | Enable leader election |
| `operator.config.toolDiscovery.enabled` | bool | `true` | List the tools of `Ready` servers into `status.tools`. Needs network access to each endpoint; sends only a server's stored upstream credential (`spec.credential`), to its pinned origin. |
| `operator.config.toolDiscovery.refreshInterval` | string | `5m` | Tool discovery refresh interval |
| `operator.cloud.enabled` | bool | `false` | Send `MCPServer` inventory and tools to Kavrynt Cloud |
| `operator.cloud.url` | string | `""` | Kavrynt Cloud base URL (https only) |
| `operator.cloud.tokenSecret.name` | string | `""` | Secret in the release namespace holding the cluster token |
| `operator.cloud.tokenSecret.key` | string | `token` | Key of the token in that Secret |
| `operator.cloud.syncInterval` | string | `60s` | Inventory sync interval |
| `operator.metrics.service.enabled` | bool | `true` | Service `<release>-operator-metrics` for the Operator's metrics port |
| `operator.metrics.serviceMonitor.enabled` | bool | `false` | Create a Prometheus Operator `ServiceMonitor` for the Operator (requires its CRDs) |
| `operator.metrics.serviceMonitor.interval` | string | `30s` | Scrape interval |
| `operator.metrics.serviceMonitor.labels` | object | `{}` | Extra `ServiceMonitor` labels |
| `operator.podSecurityContext` | object | non-root `65532`, `RuntimeDefault` seccomp | Pod security context |
| `operator.securityContext` | object | no privilege escalation, all capabilities dropped, read-only root filesystem | Container security context |
| `operator.resources` | object | requests `25m`/`64Mi`, limits `250m`/`256Mi` | Container resources |
| `operator.nodeSelector` | object | `{}` | Node selector |
| `operator.tolerations` | list | `[]` | Tolerations |
| `operator.affinity` | object | `{}` | Affinity |

Component flags behind these values are documented in
[docs/components](../../docs/components/).
