# Roadmap

This roadmap lists what the runtime does today and what comes next. Items
are in priority order and have no committed dates. Each item will be marked
done only once it is implemented and covered by the Kind end-to-end tests.

## Available today

- `MCPServer` custom resource, registered with `kubectl` or `kavryctl`.
- Operator validation with `Accepted`, `Ready`, and `ToolsDiscovered`
  conditions, and tool discovery into `status.tools`.
- Gateway HTTP routing at `/mcp/<namespace>.<name>`, credential header
  stripping, egress restrictions, and a read-only web console.
- Signed multi-architecture images and an OCI Helm chart.

## Next

1. Gateway client authentication and no token passthrough
   ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)), with the console behind it.
2. MCP Streamable HTTP and protocol-aware routing.
3. Policy decisions and tool-level authorization.
4. Audit and usage events.
5. Upgrade and rollback commands in `kavryctl`.
6. Upgrade, rollback, backup, and recovery workflows.
7. Tested high-availability deployment profiles.

## Project and community

- Apply to the CNCF Sandbox once client authentication ships and the project
  has outside maintainers and adopters.
- Grow the maintainer group beyond one organization.
- Publish an adopters list once there are production or evaluation users who
  agree to be named.

Propose roadmap changes by opening an issue.
