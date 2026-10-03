# Gateway

Routes MCP requests to `MCPServer` endpoints. Source: `cmd/gateway`,
`internal/gateway`.

## Behaviour

- Watches `MCPServer` resources through a read-only informer cache
  (`internal/gateway/kube`). A server gets a route when the operator reports
  `Ready=True`, the spec passes validation, and the transport is `http`.
- Route path: `/mcp/<namespace>.<name>[/<suffix>]`. Suffix and query string are
  appended to the endpoint URL.
- Endpoints are restricted to absolute `http`/`https` URLs without credentials
  or fragments.
- No token passthrough: `Authorization`, `Cookie`, and `Proxy-Authorization`
  are always removed before forwarding, along with hop-by-hop headers and
  headers named in `Connection`. Upstream `Set-Cookie` is dropped because
  every route shares the Gateway origin. Extra headers can be stripped with
  `--strip-request-headers`.
- Not implemented: authentication, policy, audit, token exchange. See
  ADR-0003.

## Endpoints

| Path | Purpose |
| --- | --- |
| `GET /healthz` | Liveness |
| `GET /readyz` | `200` after the `MCPServer` cache has synced once |
| `GET /version` | Build metadata |
| `GET /metrics` | Prometheus metrics: MCP traffic (below), Gateway counters, Go runtime, and process |
| `GET /v1/routes` | Current route table |
| `* /mcp/<route>/...` | Proxied MCP traffic |

## MCP traffic metrics

Recorded per proxied request, metadata only (kavrynt-cloud LLD-0002). Bodies,
tool arguments, and results are never recorded.

| Metric | Labels |
| --- | --- |
| `kavrynt_gateway_mcp_requests_total` | `route`, `method`, `tool`, `outcome` |
| `kavrynt_gateway_mcp_request_duration_seconds` (histogram) | `route`, `method`, `tool`, `outcome` |
| `kavrynt_gateway_mcp_bytes_total` | `route`, `direction` (`in`, `out`) |

- `method` comes from the `Mcp-Method` header, else the JSON-RPC `method` in
  the first 1 MiB of a POST body (`unknown` when absent or larger). Non-POST
  requests use `http_<verb>`, for example `http_get` for streams.
- `tool` comes from `Mcp-Name`, else `params.name` of `tools/call`; `none`
  otherwise. Names longer than 128 characters or outside `[A-Za-z0-9_./-]`
  become `invalid`; after 200 distinct tools per route (50 methods), new
  values become `other`.
- `outcome`: `ok`, `tool_error` (`result.isError`), `rpc_error` (JSON-RPC
  `error`), `client_error` (4xx), `upstream_error` (5xx),
  `upstream_unreachable`, `timeout`, `incomplete` (response copy failed), or
  `gateway_error` (unsupported transport or bad endpoint). JSON responses up
  to 1 MiB are inspected; event streams are not.

The chart adds `prometheus.io/*` scrape annotations by default
(`gateway.metrics.scrapeAnnotations`). Optional: a Prometheus Operator
`ServiceMonitor` (`gateway.metrics.serviceMonitor.enabled`) and the
"Kavrynt MCP Traffic" Grafana dashboard as a sidecar-labelled ConfigMap
(`gateway.metrics.dashboard.enabled`).

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--addr` | `:8080` | Listen address |
| `--watch-namespaces` | all | Comma-separated namespaces (`KAVRYNT_WATCH_NAMESPACES`) |
| `--strip-request-headers` | none | Extra headers never forwarded (`KAVRYNT_STRIP_REQUEST_HEADERS`) |
| `--request-timeout` | `30s` | Upstream request timeout |
| `--shutdown-timeout` | `10s` | Graceful shutdown timeout |

Kubernetes access comes from the in-cluster service account (or `KUBECONFIG`
when run locally). The chart grants only `get`, `list`, `watch` on
`mcpservers.kavrynt.io`.

## Run locally

```bash
go run ./cmd/gateway --addr 127.0.0.1:8080   # uses your current kubeconfig context
curl -fsS http://127.0.0.1:8080/v1/routes
```
