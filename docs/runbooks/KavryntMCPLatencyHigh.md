# KavryntMCPLatencyHigh

## Meaning

The p95 latency of calls to one route, measured by the Gateway from request
to complete response, has been above
`metrics.prometheusRule.latencyP95Seconds` (default 5s) for 15 minutes.

## Impact

Warning. Agents wait longer for that server's tools; calls that exceed the
Gateway's request timeout end as `timeout`.

## Diagnosis

Compare the route with others in the "Kavrynt MCP Traffic" Grafana dashboard
or the console's Traffic view (p50 and p95 per tool). If only some tools are
slow, the MCP server's work is slow. If every route is slow, check the
Gateway's CPU (`kubectl top pods -n kavrynt-system`) and its resource limits.
Event-stream responses count until their last event, so a streaming tool can
raise p95 by design.

## Mitigation

Scale or optimise the MCP server. If the Gateway is CPU-bound, raise
`gateway.resources` or add replicas (`gateway.autoscaling`). Raise the alert
threshold for routes whose tools are slow by design.
