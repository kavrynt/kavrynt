# KavryntMCPUpstreamErrorsHigh

## Meaning

More than `metrics.prometheusRule.errorRatio` (default 5%) of calls to one
route ended in `upstream_error` (the MCP server answered 5xx),
`upstream_unreachable` (no connection, or an address the egress guard
refuses), or `timeout` over 10 minutes, on a route with at least
`minRequestsPerSecond` traffic. The `route` label names it.

## Impact

Warning. Agents calling that MCP server see failures; other routes are not
affected.

## Diagnosis

```bash
ROUTE=<namespace>.<name>
kubectl -n "${ROUTE%%.*}" get mcpserver "${ROUTE#*.}" -o yaml   # endpoint and conditions
kubectl -n "${ROUTE%%.*}" get pods,svc,endpoints               # is the server running?
```

In the console, open the server and its Traffic tab, or filter Traffic by
the route and outcome. Then:

- `upstream_unreachable`: the endpoint's Service has no ready endpoints, the
  DNS name is wrong, or the address resolves to a link-local, metadata, or
  loopback address the Gateway refuses. A NetworkPolicy may block the
  Gateway (with `networkPolicy.mcpServers`, the Gateway is allowed; other
  policies in the namespace may not allow it).
- `upstream_error`: read the MCP server's own logs.
- `timeout`: see [KavryntMCPLatencyHigh](KavryntMCPLatencyHigh.md); the
  Gateway's `gateway.config.requestTimeout` (default 30s) bounds each call.

## Mitigation

Fix or scale the MCP server. If it must stay down, delete or edit its
`MCPServer` so agents stop being routed to it.
