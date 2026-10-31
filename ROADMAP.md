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
  overview of failing servers and tools, dashboards of the last 24 hours
  kept by the Gateway itself, and a single admin login.
- Signed multi-architecture images and an OCI Helm chart.
- `helm upgrade` and `helm rollback` that carry the `MCPServer` CRD, tested
  from the previous release on every change.
- Alerting rules for the Gateway, the Operator, MCP servers, and their tools
  (calls that keep failing, tools the Operator cannot discover), each with a
  runbook, tested with `promtool`, and the same MCP conditions on the
  console's Alerts page without Prometheus.
- A high-availability profile (several Gateway replicas with graceful drain,
  a standby Operator), tested with a rolling restart under load.
- Static upstream credentials for MCP servers that need an API key, from a
  Secret the platform team manages, pinned to each server's endpoint.
- Opt-in audit events: one hash-chained JSON line per MCP request, verified
  with `kavryctl audit verify`.
- Opt-in tool authorization policy: Rego evaluated in the Gateway, deny by
  default, from a ConfigMap.
- `kavryctl upgrade` and `kavryctl rollback`: Helm's upgrade and rollback
  with checks before and after.
- Token exchange (RFC 8693) for MCP servers that need the user: the caller's
  token is exchanged for one with the server's audience.
- MCP Streamable HTTP checks on every route: supported protocol revisions,
  no batches, and `Mcp-Method`/`Mcp-Name` headers that match the body.
- MCP client authentication, on by default with an explicit opt-out for
  trials: the Gateway is an OAuth 2.1 resource server with per-route token
  audiences and Protected Resource Metadata.
- MCP session affinity: each session goes to the server pod that started it,
  across Gateway replicas, without shared state.
- Endpoint scope: an `MCPServer` reaches only its own namespace unless the
  platform team allows a pair, and only exchanges tokens for listed
  audiences.
- Alert notifications (Slack, email, Teams, webhook), Grafana dashboards,
  and chart NetworkPolicies that make MCP servers reachable only through the
  Gateway.
- An opt-in Gateway audience: one token can reach a listed group of MCP
  routes, with policy and audit telling it apart from a per-route token.
- `kavryctl doctor`: a read-only installation report, safe to paste into a
  public issue with `--anonymize`.
- `kavryctl export` and `import`: restore registrations into a rebuilt
  cluster, checking every document before writing any.

## Next

1. Approvals for sensitive tool calls
   ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)).
2. The console signed in through the authorization server.
3. Audit delivery to Kavrynt Cloud (the Gateway side is built; it needs the
   Cloud ingestion service and an end-to-end test) and usage events.
4. More installation checks in `kavryctl doctor` (policy, audit, and
   NetworkPolicy settings).

## Project and community

- Apply to the CNCF Sandbox once the project has outside maintainers and
  adopters (client authentication has shipped).
- Grow the maintainer group beyond one organization.
- Publish an adopters list once there are production or evaluation users who
  agree to be named.

Propose roadmap changes by opening an issue.
