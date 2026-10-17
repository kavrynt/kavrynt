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
| `ToolsDiscovered` | the Operator listed the server's tools (reason `Discovered`) | `DiscoveryFailed`, `NotRoutable` |

- Lists the tools of each `Ready` server into `status.tools` (see
  [Tool discovery](#tool-discovery)).
- Optionally sends the inventory to Kavrynt Cloud (see
  [Kavrynt Cloud sync](#kavrynt-cloud-sync)).
- Does not deploy MCP server workloads, manage secrets, or proxy MCP traffic.

## Tool discovery

For each `Ready` server the Operator opens one short MCP session with
`spec.endpoint` (`initialize`, `notifications/initialized`, `tools/list`)
and records the result in `status.tools`: each tool's `name`, `description`,
and `inputSchema`. It repeats this every `--tool-refresh-interval` and when
the spec changes. `kavryctl list` shows the tool count; `kavryctl inspect`
shows the tools.

- Discovery never changes `Ready`. A server that rejects the session (for
  example because it requires credentials) or cannot be reached keeps routing;
  `ToolsDiscovered` is `False` with reason `DiscoveryFailed`, the last known
  tools are kept, and the Operator retries with backoff (30 s, doubling, up to
  the refresh interval).
- The Operator sends no credentials and follows no redirects.
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
| `--leader-elect` | `false` (chart: `true`) | Leader election through a Lease |
| `--gateway-watch-namespaces` | all (chart: `global.watchNamespaces`) | Comma-separated namespaces the Gateway watches; servers elsewhere get `Ready=False` (`NamespaceNotWatched`) |
| `--tool-discovery` | `true` (chart: `config.toolDiscovery.enabled`) | List the tools of `Ready` servers into `status.tools` |
| `--tool-refresh-interval` | `5m` (chart: `config.toolDiscovery.refreshInterval`) | How often tools are listed again |
| `--cloud-sync` | `false` (chart: `cloud.enabled`) | Send the inventory to Kavrynt Cloud |
| `--cloud-url` | none (chart: `cloud.url`) | Kavrynt Cloud base URL; https only |
| `--cloud-token-file` | `/var/run/secrets/kavrynt/cloud/token` (chart: Secret `cloud.tokenSecret`) | Cluster token, re-read before every sync |
| `--cloud-sync-interval` | `60s` (chart: `cloud.syncInterval`) | How often the inventory is sent without changes |

## Manifests

- CRD: `config/crd/bases/kavrynt.io_mcpservers.yaml`, copied to
  `charts/kavrynt/charts/k8s-operator/crds/`. Keep both identical; the release
  contract check enforces it.
- Raw kustomize manifests: `config/`. Sample: `config/samples/`.

## Run locally

```bash
kubectl apply -f config/crd/bases/kavrynt.io_mcpservers.yaml
go run ./cmd/operator   # uses your current kubeconfig context
kubectl apply -f config/samples/kavrynt_v1alpha1_mcpserver.yaml
kubectl get mcpservers
```
