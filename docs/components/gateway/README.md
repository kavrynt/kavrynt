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
- Graceful shutdown: on `SIGTERM` the Gateway answers `/readyz` with `503`
  (`draining`), stops HTTP keep-alives so clients reconnect to another
  replica, keeps serving for `--shutdown-delay`, then stops accepting
  connections and waits up to `--shutdown-timeout` for in-flight requests.
  A rolling restart of several replicas fails no MCP call (tested end to
  end).
- Not implemented: authentication, policy, audit, token exchange. See
  ADR-0003.

## High availability

The Gateway keeps no state that replicas must share: routes come from the
Kubernetes API, and console sessions are signed with the key in the mounted
admin Secret, so every replica accepts them. Run several replicas with
`gateway.replicaCount` or `gateway.autoscaling`; the chart then adds a
PodDisruptionBudget and soft spread across nodes and zones.
`examples/helm/values-production.yaml` is the tested profile.

Per replica, by design: recent traffic (`/v1/traffic`, which names the
replica in `instance`) and the console login limiter (five failed logins a
minute per client address, per replica). Use `/metrics` in Prometheus for the
cluster-wide view.

## Endpoints

| Path | Purpose |
| --- | --- |
| `GET /healthz` | Liveness |
| `GET /readyz` | `200` after the `MCPServer` cache has synced once; `503` while draining on shutdown |
| `GET /version` | Build metadata |
| `GET /metrics` | Prometheus metrics: MCP traffic (below), Gateway counters, Go runtime, and process |
| `GET /v1/routes` | Current route table |
| `GET /v1/servers` | Every watched `MCPServer`: conditions, discovered tools, the `serverInfo` it reported, whether it is routed. Leaves out `command`, `args`, `environment`, and the endpoint query. Needs a console session when console login is on |
| `GET /v1/traffic` | `instance` (this replica's name) and `requests`: the last 500 proxied requests on this replica, newest first: route, method, tool, outcome, status, latency, sizes. Needs a console session when console login is on |
| `GET`, `POST`, `DELETE /v1/session` | Console login: report the session (or `{"enabled":false}`), sign in with `{"username":"admin","password":"..."}` as JSON, sign out |
| `GET /ui/` | Web console (`--console`); `/` redirects here |
| `* /mcp/<route>/...` | Proxied MCP traffic |

## Console

A read-only web console served by the Gateway itself, enabled by default
(chart: `console.enabled`). It needs no extra RBAC and no external assets;
everything it shows comes from `/v1/servers`, `/v1/traffic`, `/readyz`, and
`/version`.

- **Overview** (the landing page): Gateway readiness and version, counts of
  servers, routed servers, and tools, and recent request volume, error rate,
  and p95 latency. **Needs attention** lists servers that are not routed,
  whose tool discovery failed, or whose recent requests mostly fail (at
  least half of at least five), with the reason. **Failing tools** lists the
  five tools with the most failed calls.
- **Servers**: every `MCPServer` with its health, recent request count and
  error rate, declared and reported version (marked when they differ),
  conditions, and discovered tools with input schemas. A connect
  panel gives the route URL and ready-to-paste MCP client, Python agent, and
  curl snippets. **Copy diagnostics** copies a metadata-only summary
  (conditions, tools, recent traffic; no tool inputs or results, and the
  endpoint's query string redacted) for an issue or a chat.
- **Tools**: every discovered tool across all servers, searchable by name,
  description, and route, with a link to try it in the Playground.
- **Playground**: opens an MCP session (`initialize`, `tools/list`) through
  the Gateway route, builds a form from each tool's input schema, and calls
  tools (`tools/call`) or any raw JSON-RPC method. JSON and event-stream
  responses are supported. Calls and results stay in the page's memory.
- **Traffic**: recent requests refreshed every two seconds, filtered by
  route, method, tool, and outcome, with per-tool calls, error rate, and
  p50/p95 latency, 100 rows at a time. **What do the outcomes mean?**
  explains each outcome (see below).

Filters are part of the URL, so a filtered view can be shared as a link.
Polling stops while the browser tab is hidden. Traffic covers only the
Gateway replica that served the page (named above the table) and its last 500
requests; use `/metrics` for history.

```bash
kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080
# open http://localhost:18080/ui/ and sign in as admin with:
kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d
```

### Console login

The console has one user, `admin`, like Argo CD's initial admin account. The
chart generates a random 32-character password and a session signing key
into the Secret `<release>-gateway-console-admin` (`kavrynt-gateway-console-admin`
for the default release), keeps them across `helm upgrade`, and keeps the
Secret on `helm uninstall`. The kubelet mounts the Secret into the Gateway;
the Gateway's service account has no RBAC on Secrets.

- Sessions are `HttpOnly`, `SameSite=Strict` cookies scoped to `/v1/` and
  last 12 hours (`console.auth.sessionTTL`). They are `Secure` when the
  request arrives over HTTPS or with `X-Forwarded-Proto: https`.
- To change the password, update the Secret's `password` key. Every session
  ends once the kubelet updates the mount (about a minute); no restart is
  needed.
- After five failed logins from one address within a minute, logins from
  that address get `429` until the minute has passed. Behind an Ingress the
  address is the Ingress, so the limit applies to everyone.
- Failed and successful logins are logged with the client address. The
  password and session tokens are never logged.
- GitOps and `helm template` renders cannot look up the existing Secret and
  would generate a new password on every render. Create your own Secret with
  `password` and `sessionKey` keys and set `console.auth.existingSecret`.
- `console.auth.enabled=false` (flag `--console-auth=false`) turns the login
  off; `/v1/servers` and `/v1/traffic` are then open as before.

**What the login does not protect.** It covers the console and the two
endpoints it reads. It is not client authentication: `/mcp/` routes stay
open, so anyone who can reach the Gateway can call every routed tool, with
or without the console. `/v1/routes`, `/healthz`, `/readyz`, `/version`, and
`/metrics` stay open too. Keep the Gateway Service internal. When the
console is opened from an address other than loopback, it shows a banner
saying that MCP routes are not authenticated.

To serve the console through an Ingress, add the Ingress origin to
`config.allowedOrigins`; the Gateway rejects logins and MCP calls from other
browser origins. Server and tool metadata is untrusted and is only rendered
as text; the console's `Content-Security-Policy` allows only its own scripts
and the Gateway's endpoints.

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
| `--console-auth` | `true` (chart: `console.auth.enabled`) | Require the console admin login for the console, `/v1/servers`, and `/v1/traffic` (with `--console`). MCP routes are not affected |
| `--console-auth-dir` | none (chart: the mounted admin Secret) | Directory with the `password` and `sessionKey` files (`KAVRYNT_CONSOLE_AUTH_DIR`). Required with `--console-auth` |
| `--console-session-ttl` | `12h` (chart: `console.auth.sessionTTL`) | Console session lifetime |
| `--allow-loopback-endpoints` | `false` | Allow endpoints that resolve to loopback; for running the Gateway on a workstation only |
| `--shutdown-delay` | `5s` (chart: `config.shutdownDelay`) | How long to keep serving after `/readyz` starts failing on shutdown |
| `--shutdown-timeout` | `10s` (chart: `config.shutdownTimeout`) | How long to wait for in-flight requests once the server stops accepting connections |
| `--instance` | `KAVRYNT_INSTANCE`, else the hostname (chart: the pod name) | Name of this replica in `/v1/traffic` |

Kubernetes access comes from the in-cluster service account (or `KUBECONFIG`
when run locally). The chart grants only `get`, `list`, `watch` on
`mcpservers.kavrynt.io`.

## Run locally

```bash
# Uses your current kubeconfig context. The console login needs a directory
# with password and sessionKey files; turn it off for local work.
go run ./cmd/gateway --addr 127.0.0.1:8080 --console-auth=false
curl -fsS http://127.0.0.1:8080/v1/routes
# console: http://127.0.0.1:8080/ui/
```

MCP servers on your workstation (for example `http://127.0.0.1:3000/mcp`)
need `--allow-loopback-endpoints`; never set it in a cluster:

```bash
go run ./cmd/gateway --addr 127.0.0.1:8080 --console-auth=false --allow-loopback-endpoints
```
