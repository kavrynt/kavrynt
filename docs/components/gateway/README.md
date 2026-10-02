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
| `GET /metrics` | Request, proxy, stripped-credential, and route-sync counters (Prometheus text) |
| `GET /v1/routes` | Current route table |
| `* /mcp/<route>/...` | Proxied MCP traffic |

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
