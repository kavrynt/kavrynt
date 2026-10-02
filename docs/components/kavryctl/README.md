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
| `kavryctl list [-n NS] [-A]` | Table with readiness and Gateway route |
| `kavryctl inspect [-n NS] <name>` | Full resource as JSON |

Cluster commands accept `--kubeconfig`, `--context`, and `-n/--namespace`.
Without `-n`, the namespace comes from the manifest, then the kubeconfig
context. A manifest namespace that conflicts with `-n` is an error.

Manifests written for `0.0.1-beta.1` (JSON with `metadata.description`) still
load; the description becomes the `kavrynt.io/description` annotation.

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
