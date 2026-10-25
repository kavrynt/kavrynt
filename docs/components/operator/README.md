# Operator

Validates `MCPServer` resources (`kavrynt.io/v1alpha1`) and reports their
state. Source: `cmd/operator`, `internal/operator`, `api/v1alpha1`.

## Behaviour

- Validates each `MCPServer` with the rules in `api/v1alpha1/validation.go`
  (the CRD enforces the same rules at admission where it can).
- Sets status conditions:

| Condition | True when | False reasons |
| --- | --- | --- |
| `Accepted` | spec is valid | `InvalidSpec` |
| `Ready` | accepted, `transport: http`, and in a namespace the Gateway watches (the Gateway can route it) | `InvalidSpec`, `UnsupportedTransport`, `NamespaceNotWatched` |
| `ToolsDiscovered` | the Operator listed the server's tools (reason `Discovered`) | `DiscoveryFailed`, `NotRoutable`, `CredentialUnavailable` (the server declares `spec.credential` and its stored credential is missing or not pinned to the endpoint), `CallerTokenRequired` (the server declares `spec.tokenExchange`: its tools are listed with a caller's token, which the Operator does not have) |

- Lists the tools of each `Ready` server into `status.tools` (see
  [Tool discovery](#tool-discovery)).
- Optionally sends the inventory to Kavrynt Cloud (see
  [Kavrynt Cloud sync](#kavrynt-cloud-sync)).
- Exports `kavrynt_operator_mcpserver_ready{namespace,name}` (1 when `Ready`
  is `True`, else 0; removed when the `MCPServer` is deleted) on the metrics
  port, which the chart exposes through the Service
  `<release>-operator-metrics` (`operator.metrics.service.enabled`, on by
  default) and optionally a ServiceMonitor. Only the leader exports it.
- Exports `kavrynt_operator_mcpserver_tools_discovered{namespace,name}` (1
  when `ToolsDiscovered` is `True`, 0 when discovery failed with
  `DiscoveryFailed` or `CredentialUnavailable`; no series while tool
  discovery is off, for servers that are not `Ready` or have
  `spec.tokenExchange`, or after the `MCPServer` is deleted). `KavryntMCPToolDiscoveryFailing` alerts on it.
- Does not deploy MCP server workloads, manage secrets, or proxy MCP traffic.

## Tool discovery

For each `Ready` server the Operator opens one short MCP session with
`spec.endpoint` (`initialize`, `notifications/initialized`, `tools/list`)
and records the result in `status.tools`: each tool's `name`, `description`,
and `inputSchema`. It also records the `serverInfo` name and version the
server reports in `initialize` in `status.serverInfo`, so the version that
actually runs can be compared with `spec.version` (which is whatever the
manifest says). It repeats this every `--tool-refresh-interval` and when
the spec changes. `kavryctl list` shows the tool count; `kavryctl inspect`
shows the tools and `serverInfo`; `kubectl get mcpservers` shows the reported
version in the `REPORTED` column.

- `serverInfo` is self-reported and untrusted. A name or version is kept only
  when it is a non-blank string of at most 128 printable characters. A failed
  discovery keeps the last value while the endpoint is unchanged; a changed
  endpoint or a server that is not `Ready` has none until it next answers.

- Discovery never changes `Ready`. A server that rejects the session (for
  example because it requires credentials) or cannot be reached keeps routing;
  `ToolsDiscovered` is `False` with reason `DiscoveryFailed`, the last known
  tools are kept, and the Operator retries with backoff (30 s, doubling, up to
  the refresh interval).
- The Operator sends no credentials except a server's stored upstream
  credential (`spec.credential`), only to its pinned origin, and follows no
  redirects.
- It never connects to unspecified, link-local (including
  `169.254.169.254`), multicast, cloud metadata, or loopback addresses; such
  servers get `DiscoveryFailed`. `--allow-loopback-endpoints` permits
  loopback when the Operator runs on a workstation.
- Tool metadata comes from the MCP server and is untrusted. Limits: 10 s per
  session, 1 MiB per response, 5 `tools/list` pages, 200 tools; names up to
  128 characters of `[A-Za-z0-9_./-]` (others are skipped); descriptions cut to
  1024 characters; input schemas kept only when they are JSON objects of at
  most 16 KiB; at most 256 KiB of tool data per server.
- The Operator needs network access to each server's endpoint. Where it must
  not reach MCP servers, disable discovery (`config.toolDiscovery.enabled:
  false` in the operator subchart, `operator.config.toolDiscovery.enabled` in
  the umbrella chart).

## Kavrynt Cloud sync

Off unless `--cloud-sync` is set (chart: `cloud.enabled`). The leader replica
sends a full snapshot of every `MCPServer` to `POST <cloud-url>/v1/sync/mcpservers`
at start, every `--cloud-sync-interval`, and 5 s after a change.

- Each server is sent with its name, namespace, transport, endpoint (query and
  fragment removed), `spec.version`, namespace as owner, status (`ready` or
  `not_ready` from the `Ready` condition), and `status.tools`. `spec.args`,
  `spec.environment`, and annotations are never sent, nor are tenant or
  cluster IDs: Cloud derives them from the token.
- The snapshot is the whole inventory: Cloud marks servers and tools it no
  longer lists as removed. A snapshot over 1 MiB (Cloud's request limit) is
  sent without tool input schemas, then without descriptions; if it still does
  not fit, it is not sent and the sync fails, because a partial snapshot would
  remove servers in Cloud.
- The cluster token is read from `--cloud-token-file` before every sync, so a
  rotated Secret takes effect without a restart. It is never logged. Requests
  are HTTPS only, follow no redirects, and time out after 30 s.
- Failures back off exponentially with jitter (5 s up to 5 min) and honour
  `Retry-After` on 429 and 503.
- Metrics: `kavrynt_operator_cloud_sync_success_total`,
  `kavrynt_operator_cloud_sync_failure_total{reason}` (`token`, `inventory`,
  `too_large`, `network`, `rejected`, `throttled`, `server_error`), and
  `kavrynt_operator_cloud_sync_last_success_timestamp_seconds`.

Chart setup (umbrella chart: prefix with `operator.`):

```bash
kubectl -n kavrynt-system create secret generic kavrynt-cloud-token --from-literal=token="$CLUSTER_TOKEN"
helm upgrade --install kavrynt charts/kavrynt -n kavrynt-system \
  --set operator.cloud.enabled=true \
  --set operator.cloud.url="$KAVRYNT_CLOUD_URL" \
  --set operator.cloud.tokenSecret.name=kavrynt-cloud-token
```

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--metrics-bind-address` | `:8080` | controller-runtime metrics |
| `--health-probe-bind-address` | `:8081` | `/healthz`, `/readyz` |
| `--leader-elect` | `false` (chart: `true`) | Leader election through the Lease `kavrynt-k8s-operator`. Only the leader reconciles, discovers tools, and syncs to Kavrynt Cloud; a stopping leader releases the Lease so a standby takes over at once. The chart refuses more than one replica without it and adds a PodDisruptionBudget for several |
| `--upstream-credentials-dir` | none (chart: the mounted `global.upstreamCredentials.secretName`) | Directory of the upstream credentials Secret; discovery sends a server's stored credential like the Gateway ([Upstream credentials](../gateway/README.md#upstream-credentials)) |
| `--gateway-watch-namespaces` | all (chart: `global.watchNamespaces`) | Comma-separated namespaces the Gateway watches; servers elsewhere get `Ready=False` (`NamespaceNotWatched`) |
| `--tool-discovery` | `true` (chart: `config.toolDiscovery.enabled`) | List the tools of `Ready` servers into `status.tools` |
| `--tool-refresh-interval` | `5m` (chart: `config.toolDiscovery.refreshInterval`) | How often tools are listed again |
| `--allow-loopback-endpoints` | `false` | Allow discovery against loopback endpoints; for running the Operator on a workstation only |
| `--cloud-sync` | `false` (chart: `cloud.enabled`) | Send the inventory to Kavrynt Cloud |
| `--cloud-url` | none (chart: `cloud.url`) | Kavrynt Cloud base URL; https only |
| `--cloud-token-file` | `/var/run/secrets/kavrynt/cloud/token` (chart: Secret `cloud.tokenSecret`) | Cluster token, re-read before every sync |
| `--cloud-sync-interval` | `60s` (chart: `cloud.syncInterval`) | How often the inventory is sent without changes |

## Manifests

- CRD: `config/crd/bases/kavrynt.io_mcpservers.yaml`, copied to
  `charts/kavrynt/charts/k8s-operator/files/`. Keep both identical; the release
  contract check enforces it. The chart renders the copy as a template, so
  `helm upgrade` updates the CRD, and keeps it on uninstall and rollback
  (`operator.crds.install`, `operator.crds.keep`).
- Raw kustomize manifests: `config/`. Sample: `config/samples/`.

## Run locally

```bash
kubectl apply -f config/crd/bases/kavrynt.io_mcpservers.yaml
go run ./cmd/operator   # uses your current kubeconfig context
kubectl apply -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
```
