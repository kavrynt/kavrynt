# kavryctl

Client CLI for `MCPServer` resources. Source: `cmd/kavryctl`,
`internal/kavryctl`.

## Commands

| Command | Purpose |
| --- | --- |
| `kavryctl version` | Build metadata |
| `kavryctl validate <file>` | Offline validation of a YAML or JSON `MCPServer` manifest |
| `kavryctl register [-n NS] <file>` | Create or update the `MCPServer` |
| `kavryctl unregister [-n NS] <name>` | Delete the `MCPServer` |
| `kavryctl list [-n NS] [-A]` | Table with readiness, Gateway route (`-` until the server is `Ready`), and `TOOLS`: the tool count once the Operator has listed the tools, the `ToolsDiscovered` reason (such as `DiscoveryFailed` or `CallerTokenRequired`) when it could not, and `-` before it tried. An empty list names the namespace it searched and `kavryctl list -A`, with the count in other namespaces when you may list them |
| `kavryctl inspect [-n NS] <name>` | Full resource as JSON |
| `kavryctl doctor [-n NS] [--release R] [--output json] [--anonymize]` | Read-only check of the installation (namespace `kavrynt-system` and release `kavrynt` by default): the CRD, the Gateway and Operator Deployments and versions, MCP client authentication, and every `MCPServer`'s readiness and tool discovery. Each warning or failure says what to do and links its runbook; checks you may not run are `skipped`. Exit `0`, `1` when a check failed, `2` when the cluster cannot be reached. Never prints condition messages, endpoints, or Secret data; `--anonymize` replaces namespaces and names with per-report HMACs for public issues (SPEC-0033) |
| `kavryctl upgrade --version V` | Upgrade the Kavrynt Helm release with checks before and after (below) |
| `kavryctl rollback [--revision N]` | Roll the release back with the same checks |
| `kavryctl audit verify [file]` | Verify Gateway audit event chains from a file or standard input; other log lines are skipped. Prints each break (gap, reordering, altered event) and exits `1` when there is one. Events from a newer Gateway verify as written; their unknown fields are counted, and a break of such an event names them instead of saying altered (SPEC-0035). It cannot detect events cut off the end of a chain, or a chain rewritten with recomputed hashes; see [what verify proves](../gateway/README.md#audit-events) |

Cluster commands accept `--kubeconfig`, `--context`, and `-n/--namespace`.
Without `-n`, the namespace comes from the manifest, then the kubeconfig
context. A manifest namespace that conflicts with `-n` is an error.

## Upgrade and rollback

`kavryctl upgrade` and `kavryctl rollback` run the `helm` binary (it must be
on `PATH`) with the tested upgrade path (SPEC-0019). They act on release
`kavrynt` in `kavrynt-system` unless `--release` and `-n` say otherwise, and
pass `--context` and `--kubeconfig` to Helm.

Before changing anything, `upgrade` checks that the release exists, that the
chart version exists (`--chart`, default `oci://ghcr.io/kavrynt/charts/kavrynt`;
`--version` is required unless `--chart` is a chart directory), and that the
release owns the `MCPServer` CRD. A CRD installed by `0.0.5-beta.1` or
earlier stops the upgrade with the two `kubectl` commands that adopt it, or
is adopted with `--adopt-crd`. It records the servers that are routed
(`Ready`), runs `helm upgrade --reset-then-reuse-values [--values FILE] --wait`
(Helm 3.14 or newer; `--reuse-values` would keep the previous chart's
defaults, including its image tags), then
waits up to `--timeout` (default `5m`) until each of them is routed again
and prints one line per server. It exits `1` if any is not. `rollback`
checks the release, runs `helm rollback [N] --wait`, and does the same
check after. `--dry-run` runs the checks and prints the Helm command
without changing anything. Values are never printed.

```bash
kavryctl upgrade --version 1.0.0-beta.3 --adopt-crd
kavryctl rollback --revision 3
```

GitOps-managed installs upgrade through their own pipeline instead.

## Examples

```bash
kavryctl validate examples/kavryctl/mcp-server.yaml
kavryctl register -n default examples/kavryctl/mcp-server.yaml
kavryctl list -A
kavryctl inspect -n default example-mcp-server
kavryctl unregister -n default example-mcp-server
```

## Install

Release archives (Linux, macOS, Windows; amd64 and arm64) ship with a
`SHA256SUMS` file signed by Cosign (`SHA256SUMS.sigstore.json`). From source:

```bash
go install ./cmd/kavryctl
```
