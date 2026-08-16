# kavryctl

`kavryctl` is the Kavrynt command-line interface.

This first implementation slice focuses on MCP server metadata. It supports
both local file-backed registration and remote registration through the
Kavrynt Registry API. It is intentionally small so the product can validate CLI
shape, manifest validation, and registry semantics before introducing Gateway,
Kubernetes Operator, or policy enforcement.

## Development Model

Kavrynt uses GitFlow:

- `main` is production-ready code.
- `develop` is the latest integrated development branch.
- `feature/<branch-name>` is used for each focused feature.

Repository coding, branching, and security standards are defined in
[AGENTS.md](AGENTS.md).

## Current Commands

```bash
kavryctl version
kavryctl init [--home DIR]
kavryctl validate <manifest.json>
kavryctl register [--home DIR] [--registry URL] <manifest.json>
kavryctl unregister [--home DIR] [--registry URL] <server-id>
kavryctl list [--home DIR] [--registry URL]
kavryctl inspect [--home DIR] [--registry URL] <server-id>
```

By default, local state is stored in `.kavrynt/registry.json` under the current
directory. Override this with `--home DIR` or `KAVRYNT_HOME`.

Use `--registry URL` or `KAVRYNT_REGISTRY_URL` to use the remote Kavrynt
Registry API instead.

## Install

From GitHub Releases on Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.sh | sh
```

From PowerShell on Windows:

```powershell
iwr https://raw.githubusercontent.com/kavrynt/kavrynt/main/scripts/install.ps1 -UseB | iex
```

From source:

```bash
go install ./cmd/kavryctl
```

## Quick Start

```bash
go test ./...
go run . version
go run . init
go run . validate examples/mcp-server.json
go run . register examples/mcp-server.json
go run . list
go run . inspect example-mcp-server
go run . unregister example-mcp-server
```

## Remote Registry Workflow

Start Kavrynt Registry separately, then point `kavryctl` at it:

```bash
export KAVRYNT_REGISTRY_URL=http://localhost:8080
go run . register examples/mcp-server.json
go run . list
go run . inspect example-mcp-server
go run . unregister example-mcp-server
```

Or pass the Registry URL explicitly:

```bash
go run . register --registry http://localhost:8080 examples/mcp-server.json
go run . list --registry http://localhost:8080
go run . inspect --registry http://localhost:8080 example-mcp-server
go run . unregister --registry http://localhost:8080 example-mcp-server
```

`--home` and `--registry` are mutually exclusive. Use `--home` for local
file-backed workflows and `--registry` for shared Registry API workflows.

For Docker, Helm, GitHub Actions, and first-time contributor steps, see
[docs/RUNBOOK.md](docs/RUNBOOK.md).

## Manifest Shape

The first manifest format is JSON:

```json
{
  "apiVersion": "kavrynt.io/v1alpha1",
  "kind": "MCPServer",
  "metadata": {
    "name": "example-mcp-server"
  },
  "spec": {
    "version": "0.1.0",
    "transport": "stdio",
    "command": "python3",
    "args": ["-m", "example_mcp_server"]
  }
}
```

Supported transports in this slice:

- `stdio`: requires `spec.command`
- `http`: requires `spec.endpoint`

## Scope

In scope:

- local CLI scaffold
- JSON MCP server manifest validation
- local registry initialization
- remote Registry API registration
- register/list/inspect workflows
- unregister workflow
- tests for validation and registry behavior

Out of scope for this slice:

- Gateway runtime traffic
- Kubernetes Operator
- authentication and authorization enforcement
- policy engine
- upgrade and rollback execution
