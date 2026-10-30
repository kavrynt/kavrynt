# KavryntMCPToolDiscoveryFailing

## Meaning

The Operator could not list the tools of a `Ready` `MCPServer` (labels
`namespace`, `name`) for `metrics.prometheusRule.discoveryFailingFor`
(default 15m): its `ToolsDiscovered` condition is `False` with reason
`DiscoveryFailed` or `CredentialUnavailable`.

Servers with `spec.tokenExchange` are never listed by design
(`CallerTokenRequired`) and do not raise this alert.

## Impact

Warning. The Gateway still routes the server, so agents can call it, but
`status.tools`, the console's Tools page, and the Kavrynt Cloud inventory
keep the last tools discovered from the same endpoint, or none.

## Diagnosis

```bash
kubectl -n <namespace> get mcpserver <name>   # the Discovery column
kubectl -n <namespace> get mcpserver <name> -o jsonpath='{range .status.conditions[?(@.type=="ToolsDiscovered")]}{.reason}: {.message}{"\n"}{end}'
```

- `DiscoveryFailed`: the message names the cause of the failed MCP
  `initialize` or `tools/list` call and what to check
  ([all messages](../components/operator/README.md#discovery-messages)):
  - `cannot connect to <host>:<port>`: the Service has no ready pods, or
    nothing listens on that port. Check
    `kubectl -n <namespace> get endpointslices -l kubernetes.io/service-name=<service>`
    and the pods' readiness.
  - `host <host> does not resolve`: the Service name or namespace in
    `spec.endpoint` is wrong, or the Service does not exist.
  - `HTTP 404 for path "<path>"`: the server serves MCP at another path,
    often `/mcp`.
  - `HTTP 401` or `HTTP 403`: the server needs a credential. Set
    `spec.credential` for a stored key, or `spec.tokenExchange` for the
    caller's own token.
  - `the egress policy refuses <kind> addresses`: the endpoint resolves to a
    link-local, cloud metadata, or loopback address, which the Operator and
    the Gateway never connect to. Point it at the server's Service.
  - `no answer ... within 10s`: the server did not answer `initialize` in
    time, or the connection never opened, for example because a
    NetworkPolicy in the server's namespace drops the Operator's traffic.
  - `does not look like an MCP Streamable HTTP endpoint`: something
    answered, but not with MCP JSON-RPC; the endpoint may point at a web page
    or another service.
- `CredentialUnavailable`: `spec.credential` is set but the upstream
  credentials Secret has no key `<namespace>.<name>`, or its
  `<namespace>.<name>.origin` does not match the endpoint. The message names
  the key, never the value.

The Operator retries with backoff, so the message is the latest attempt's.

## Mitigation

Fix the MCP server so `tools/list` answers (the Operator retries with
backoff and recovers on its own), or add or correct the credential in
`global.upstreamCredentials.secretName`. If the server's tools are not
meant to be listed, set `operator.config.toolDiscovery.enabled=false` for
the whole Operator.
