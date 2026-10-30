# End-to-End Tests

All tests create a disposable Kind cluster, never touch your current
Kubernetes context, and delete the cluster afterwards (`KEEP_CLUSTER=1` keeps
it for debugging). Prerequisites: Docker, Kind, `kubectl`, Helm, `jq`, Go.
`make e2e-kind` creates its cluster from `test/e2e/kind-config.yaml`, whose
kubelet syncs every 10 seconds, so updated ConfigMap and Secret mounts reach
pods in seconds.

In CI the Kind e2e runs for every pull request that changes more than
Markdown, `specs/`, or `docs/`, at the same time as Go QA. Locally, run it
once on the top of a stack of branches rather than on each one.

| Target | What it proves | Images |
| --- | --- | --- |
| `make e2e-kind` | Current source works end to end | Built locally |
| `make e2e-release RELEASE_VERSION=<v>` | Published artifacts work | Pulled from Docker Hub; chart from GHCR |

## `make e2e-kind`

1. Builds `kavryctl`, the Gateway and Operator images, and a sample MCP server.
2. Confirms an install with default values is refused with a message naming
   `gateway.auth.issuers` and `gateway.auth.enabled=false` and leaves no
   release (SPEC-0014). Installs `charts/kavrynt` into `kavrynt-system` with
   the explicit opt-out (the Gateway logs that MCP routes are open) and the
   production profile
   (`examples/helm/values-production.yaml`: three Gateway and two Operator
   replicas) and both NetworkPolicies on (the sample server's pods carry
   `kavrynt.io/gateway-only: "true"`; only `kavrynt-e2e` may call the
   Gateway), and confirms the chart
   installed the `MCPServer` CRD of `config/crd/bases`, owned by Helm with
   the keep policy.
3. Applies `test/e2e/manifests/mcp-server.yaml` and waits for `Ready`.
4. Confirms the CRD rejects an endpoint with embedded credentials.
5. Confirms Gateway RBAC: `get/list/watch` on `MCPServer` and EndpointSlices
   only; no writes, no Secrets.
6. Confirms the Operator records the sample server's `echo` tool and input
   schema in `status.tools`, and that a server it cannot reach stays `Ready`
   with `ToolsDiscovered=False` (`DiscoveryFailed`).
7. Calls `tools/list` and `tools/call` through
   `/mcp/kavrynt-e2e.example-mcp-server` and checks the MCP traffic metrics,
   including that tool arguments never appear in them.
8. Confirms the Gateway rejects a cross-origin browser request (`403`),
   answers `/v1/servers` and `/v1/traffic` with `401` until it signs in with
   the generated console admin password, serves the console, lists the
   server and its tools at `/v1/servers`, records the tool call at
   `/v1/traffic` without its arguments, finds it at `/v1/history` for its
   route and tool within a minute (and `range=7d` refused), lists an unreachable `MCPServer` as
   `DiscoveryFailing` at `/v1/alerts` (with `discoveryFailingFor=30s`) until
   it is deleted, runs the console browser smoke test
   (`test/console-smoke`, when installed; always in CI), and refuses
   an `MCPServer` whose endpoint is `169.254.169.254` (`502`, and
   `DiscoveryFailed` in the Operator).
9. Registers, inspects, routes to, and unregisters a second server with
   `kavryctl`.
10. Bypass protection: a pod calling the sample MCP server directly, even
    from its own namespace, and a pod in `default` calling the Gateway are
    both blocked, while a pod in `kavrynt-e2e` reaches the server through
    the Gateway. High availability: `/v1/traffic` names the answering Gateway pod; a
    client pod calls `tools/list` through the Gateway Service throughout a
    rolling restart of every Gateway replica and no call may fail; deleting
    the Operator leader lets the standby reconcile a new server to `Ready`.
    Session affinity: ten agents with twenty calls each, a new connection
    per request, keep every session through the Gateway replicas to a
    stateful three-replica server with `sessionAffinity: MCPSession`, whose
    tools are discovered from one pod; without it, sessions are lost and
    counted as `session_not_found`.
    Upstream credentials: a server that requires its own bearer token is
    discovered and routed with the stored credential from the pre-created
    Secret (the caller's token is replaced), a server whose credential is not
    stored answers `503` with `CredentialUnavailable` discovery, neither
    component can read Secrets, and the value appears in no response,
    status, metric, or log. The CRD rejects a `Cookie` credential header.
    MCP client authentication: deploys the e2e image's test authorization
    server, turns `gateway.auth` on, and checks the `401` challenge and the
    route's Protected Resource Metadata, that a valid token is accepted and
    not forwarded, that tokens for another route, without the required scope,
    or in the query string are refused, that refusals are recorded, and that
    no token appears in metrics or logs. Token exchange: a server with its
    own audience is `Ready` with `CallerTokenRequired` discovery, answers
    `tools/list`, and receives a token with its audience, the caller's
    subject, and the calling agent as `act`, never the caller's token; no
    token or client secret appears in logs or metrics. Tool authorization policy: a
    per-client allow-list from a ConfigMap allows one client's `echo` call
    and denies another's with a JSON-RPC error, and with
    `filterToolsList` the first client lists `echo` while the other lists
    no tools; a broken update is counted
    and changes no decision. Audit (on in the production profile): the
    allowed, denied, and unauthenticated calls each have an event, no tool
    argument appears in the Gateway output, and `kavryctl audit verify`
    accepts the collected logs. Then turns authentication and policy off.
    Then deletes the `MCPServer` and confirms the route disappears.
11. Upgrade and rollback, from published charts (`UPGRADE_FROM_CHART`) with
    images from Docker Hub; needs network access to the registries.
    `helm uninstall` keeps the CRD and `MCPServer` resources.
    - From `LEGACY_FROM_VERSION` (`0.0.5-beta.1`, the last release with the
      CRD outside Helm and no `gateway.auth`), installed on a clean cluster:
      `helm upgrade` to the local build without `gateway.auth` values is
      refused before any change (SPEC-0014); with the opt-out it fails
      before touching workloads until the CRD is adopted, then succeeds with
      the server `Ready` and routed.
    - From `UPGRADE_FROM_VERSION` (the previous release), installed with the
      opt-out on the kept CRD and server: `kavryctl upgrade --dry-run`
      changes nothing, `kavryctl upgrade` to the build under test reports
      the server routed (SPEC-0019), and `kavryctl rollback` back to it keeps
      the CRD, the server, and its route.

Expected final line: `==> end-to-end workflow passed`.

## CI

`.github/workflows/qa.yml` runs `e2e-kind` on pull requests
and manual dispatch. `.github/workflows/release.yml` runs `e2e-release` after
publishing.

## Troubleshooting

- `Kind cluster ... already exists`: delete it or set `KIND_CLUSTER_NAME`.
- Image pulls time out (`TLS handshake timeout` from Docker Hub): retry; base
  images are pulled during the build.
- On failure, the scripts print pods, events, and component logs before
  deleting the cluster.
