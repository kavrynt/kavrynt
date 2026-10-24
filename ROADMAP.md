# Roadmap

This roadmap lists what the runtime does today and what comes next. Items
are in priority order and have no committed dates. Each item will be marked
done only once it is implemented and covered by the Kind end-to-end tests.

## Available today

- `MCPServer` custom resource, registered with `kubectl` or `kavryctl`.
- Operator validation with `Accepted`, `Ready`, and `ToolsDiscovered`
  conditions, and tool discovery into `status.tools`.
- Gateway HTTP routing at `/mcp/<namespace>.<name>`, credential header
  stripping, egress restrictions, and a read-only web console with an
  overview of failing servers and tools and a single admin login.
- Signed multi-architecture images and an OCI Helm chart.
- `helm upgrade` and `helm rollback` that carry the `MCPServer` CRD, tested
  from the previous release on every change.
- Alerting rules for the Gateway, the Operator, and MCP servers, each with a
  runbook, tested with `promtool`.
- A high-availability profile (several Gateway replicas with graceful drain,
  a standby Operator), tested with a rolling restart under load.
- Opt-in MCP client authentication: the Gateway is an OAuth 2.1 resource
  server with per-route token audiences and Protected Resource Metadata.

## Next

1. Upstream credentials and token exchange
   ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)).
2. Policy decisions and tool-level authorization.
3. Audit and usage events.
4. MCP client authentication on by default (`1.0.0-beta.1`), with the
   console behind it.
5. MCP Streamable HTTP and protocol-aware routing.
6. Upgrade and rollback commands in `kavryctl`.
7. Recovery workflows (restoring into a new cluster).

## Project and community

- Apply to the CNCF Sandbox once client authentication ships and the project
  has outside maintainers and adopters.
- Grow the maintainer group beyond one organization.
- Publish an adopters list once there are production or evaluation users who
  agree to be named.

Propose roadmap changes by opening an issue.
