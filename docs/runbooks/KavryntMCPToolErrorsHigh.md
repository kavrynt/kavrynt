# KavryntMCPToolErrorsHigh

## Meaning

More than `metrics.prometheusRule.toolErrorRatio` (default 20%) of the
`tools/call` requests for one tool (labels `route`, `tool`) answered with a
tool error (`result.isError`, outcome `tool_error`) or a JSON-RPC error
(outcome `rpc_error`) over 10 minutes, for 10 minutes, with at least
`minToolRequestsPerSecond` (default 0.02) calls to that tool.

The server itself answered: transport and HTTP failures raise
`KavryntMCPUpstreamErrorsHigh` instead.

## Impact

Warning. Agents calling that tool mostly get errors; other tools on the same
server may work.

## Diagnosis

Check the tool's error share and which outcome dominates:

```promql
sum by (outcome) (rate(kavrynt_gateway_mcp_requests_total{route="<route>",method="tools/call",tool="<tool>"}[10m]))
```

- `tool_error`: the tool ran and reported a failure, often a broken
  dependency of the MCP server (database, API key, quota) or arguments the
  tool rejects.
- `rpc_error`: the server refused the call, for example an unknown tool
  name (the tool was renamed or removed) or invalid parameters.

Compare the tool with what the Operator discovered:

```bash
kubectl -n <namespace> get mcpserver <name> -o jsonpath='{range .status.tools[*]}{.name}{"\n"}{end}'
```

The console's Traffic page lists recent calls to the tool with their
outcome; its Tools page shows the tool's schema. The server's own logs name
the failure.

`tool="other"` groups calls to tools beyond the first 200 seen on a route,
and `tool="invalid"` calls whose tool name is not a valid label; find the
real tool in the console's Traffic page.

## Mitigation

Fix the MCP server or its dependency. When one client sends bad arguments,
fix that client. When the tool was removed, update the agents that still
call it. If the error share is expected for this tool, raise
`toolErrorRatio`.
