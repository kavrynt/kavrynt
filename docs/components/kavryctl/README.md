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
| `kavryctl list [-n NS] [-A]` | Table with readiness, Gateway route (`-` until the server is `Ready`), and tool count (`-` until the Operator has listed the tools) |
| `kavryctl inspect [-n NS] <name>` | Full resource as JSON |
| `kavryctl upgrade --version V` | Upgrade the Kavrynt Helm release with checks before and after (below) |
| `kavryctl rollback [--revision N]` | Roll the release back with the same checks |
| `kavryctl audit verify [file]` | Verify Gateway audit event chains from a file or standard input; other log lines are skipped. Prints each break (gap, reordering, altered event) and exits `1` when there is one |

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
kavryctl upgrade --version 1.0.0-beta.2 --adopt-crd
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
