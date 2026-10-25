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
kubectl -n <namespace> get mcpserver <name> -o jsonpath='{range .status.conditions[?(@.type=="ToolsDiscovered")]}{.reason}: {.message}{"\n"}{end}'
```

- `DiscoveryFailed`: the message is the error of the MCP `initialize` or
  `tools/list` call, such as an HTTP status, a timeout, a refused
  address (link-local, cloud metadata, or loopback), or an answer that
  is not a valid tool list.
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
