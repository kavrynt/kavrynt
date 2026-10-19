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
- Egress guard: the Gateway never connects to unspecified, link-local
  (including `169.254.169.254`), multicast, or known cloud metadata addresses
  (`fd00:ec2::254`, `100.100.100.200`), nor to loopback unless
  `--allow-loopback-endpoints` is set. The check runs on the resolved address
  at connect time, so DNS names and rebinding cannot bypass it. Refused
  requests return `502`.
- Upstream redirects are passed to the client, never followed.
- Path suffixes with `.` or `..` segments are rejected with `400`, so callers
  cannot leave the endpoint's path.
- A request that already carries `X-Kavrynt-Route` (an endpoint pointing back
  at a Gateway) is rejected with `508` instead of looping.
- Browser protection: requests whose `Origin` is not loopback or listed in
  `--allowed-origins` are rejected with `403` (MCP DNS-rebinding guidance).
  Clients that are not browsers send no `Origin` and are unaffected.
- Proxied responses carry `Content-Security-Policy: sandbox` and
  `X-Content-Type-Options: nosniff`, so upstream content cannot run scripts
  on the Gateway origin, which the console shares.
- Upstream failures return a fixed message (`upstream request failed`, or
  `504` with `upstream request timed out`) that does not reveal the endpoint.
- `--request-timeout` bounds each request until its response is complete.
  `text/event-stream` responses are bounded only until their headers arrive,
  then flushed event by event for as long as the client stays connected.
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
| `GET /v1/servers` | Every watched `MCPServer`: conditions, discovered tools, whether it is routed. Leaves out `command`, `args`, `environment`, and the endpoint query |
| `GET /v1/traffic` | The last 500 proxied requests on this replica, newest first: route, method, tool, outcome, status, latency, sizes |
| `GET /ui/` | Web console (`--console`); `/` redirects here |
| `* /mcp/<route>/...` | Proxied MCP traffic |

## Console

A read-only web console served by the Gateway itself, enabled by default
(chart: `console.enabled`). It needs no extra RBAC and no external assets;
everything it shows comes from `/v1/servers`, `/v1/traffic`, `/readyz`, and
`/version`.

- **Servers**: every `MCPServer` with its readiness, the reason it is not
  routed, conditions, and discovered tools with input schemas. A connect panel
  gives the route URL and ready-to-paste MCP client, Python agent, and curl
  snippets.
- **Playground**: opens an MCP session (`initialize`, `tools/list`) through
  the Gateway route, builds a form from each tool's input schema, and calls
  tools (`tools/call`) or any raw JSON-RPC method. JSON and event-stream
  responses are supported.
- **Live traffic**: recent requests refreshed every two seconds, with
  per-route request count, error rate, and p50/p95 latency.

```bash
kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080
# open http://localhost:18080/ui/
```

The console has the Gateway's security boundary: it does not authenticate
users, so anyone who can reach the Gateway can use it, exactly as they can
call `/mcp/` routes. Keep the Service internal. To serve it through an
Ingress, add the Ingress origin to `config.allowedOrigins`. Server and tool
metadata is untrusted and is only rendered as text; the console's
`Content-Security-Policy` allows only its own scripts and the Gateway's
endpoints. `/v1/traffic` is per replica and lost on restart; use `/metrics`
for durable monitoring.

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
| `--watch-namespaces` | all (chart: `global.watchNamespaces`) | Comma-separated namespaces (`KAVRYNT_WATCH_NAMESPACES`) |
| `--strip-request-headers` | none | Extra headers never forwarded (`KAVRYNT_STRIP_REQUEST_HEADERS`) |
| `--request-timeout` | `30s` | Upstream request timeout; event streams only until response headers |
| `--allowed-origins` | none (chart: `config.allowedOrigins`) | Comma-separated browser origins accepted besides loopback (`KAVRYNT_ALLOWED_ORIGINS`) |
| `--console` | `true` (chart: `console.enabled`) | Serve the web console at `/ui/` |
| `--allow-loopback-endpoints` | `false` | Allow endpoints that resolve to loopback; for running the Gateway on a workstation only |
| `--shutdown-timeout` | `10s` | Graceful shutdown timeout |

Kubernetes access comes from the in-cluster service account (or `KUBECONFIG`
when run locally). The chart grants only `get`, `list`, `watch` on
`mcpservers.kavrynt.io`.

## Run locally

```bash
go run ./cmd/gateway --addr 127.0.0.1:8080   # uses your current kubeconfig context
curl -fsS http://127.0.0.1:8080/v1/routes
# console: http://127.0.0.1:8080/ui/
```

MCP servers on your workstation (for example `http://127.0.0.1:3000/mcp`)
need `--allow-loopback-endpoints`; never set it in a cluster:

```bash
go run ./cmd/gateway --addr 127.0.0.1:8080 --allow-loopback-endpoints
```
