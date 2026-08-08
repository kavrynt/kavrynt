# kavryctl

`kavryctl` is the Kavrynt command-line interface.

This first implementation slice focuses on a local, file-backed registry for
MCP server metadata. It is intentionally small so the product can validate CLI
shape, manifest validation, and registry semantics before introducing Gateway,
Kubernetes Operator, or remote control-plane behavior.

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
kavryctl register [--home DIR] <manifest.json>
kavryctl list [--home DIR]
kavryctl inspect [--home DIR] <name>
```

By default, local state is stored in `.kavrynt/registry.json` under the current
directory. Override this with `--home DIR` or `KAVRYNT_HOME`.

## Quick Start

```bash
go test ./...
go run . version
go run . init
go run . validate examples/mcp-server.json
go run . register examples/mcp-server.json
go run . list
go run . inspect example-mcp-server
```

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
- register/list/inspect workflows
- tests for validation and registry behavior

Out of scope for this slice:

- Gateway runtime traffic
- Kubernetes Operator
- remote Registry service
- authentication and authorization enforcement
- policy engine
- upgrade and rollback execution
