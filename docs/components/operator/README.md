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

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--metrics-bind-address` | `:8080` | controller-runtime metrics |
| `--health-probe-bind-address` | `:8081` | `/healthz`, `/readyz` |
| `--leader-elect` | `false` (chart: `true`) | Leader election through a Lease |
| `--gateway-watch-namespaces` | all (chart: `global.watchNamespaces`) | Comma-separated namespaces the Gateway watches; servers elsewhere get `Ready=False` (`NamespaceNotWatched`) |
| `--tool-discovery` | `true` (chart: `config.toolDiscovery.enabled`) | List the tools of `Ready` servers into `status.tools` |
| `--tool-refresh-interval` | `5m` (chart: `config.toolDiscovery.refreshInterval`) | How often tools are listed again |

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
