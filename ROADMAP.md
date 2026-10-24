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
- Static upstream credentials for MCP servers that need an API key, from a
  Secret the platform team manages, pinned to each server's endpoint.
- Opt-in audit events: one hash-chained JSON line per MCP request, verified
  with `kavryctl audit verify`.
- Opt-in tool authorization policy: Rego evaluated in the Gateway, deny by
  default, from a ConfigMap.
- MCP client authentication, on by default with an explicit opt-out for
  trials: the Gateway is an OAuth 2.1 resource server with per-route token
  audiences and Protected Resource Metadata.

## Next

1. Token exchange for upstream servers
   ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)).
2. The console signed in through the authorization server.
3. Audit delivery to Kavrynt Cloud and usage events.
4. MCP Streamable HTTP and protocol-aware routing.
5. Upgrade and rollback commands in `kavryctl`.
6. Recovery workflows (restoring into a new cluster).

## Project and community

- Apply to the CNCF Sandbox once client authentication ships and the project
  has outside maintainers and adopters.
- Grow the maintainer group beyond one organization.
- Publish an adopters list once there are production or evaluation users who
  agree to be named.

Propose roadmap changes by opening an issue.
