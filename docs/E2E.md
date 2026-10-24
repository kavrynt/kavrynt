# End-to-End Tests

All tests create a disposable Kind cluster, never touch your current
Kubernetes context, and delete the cluster afterwards (`KEEP_CLUSTER=1` keeps
it for debugging). Prerequisites: Docker, Kind, `kubectl`, Helm, `jq`, Go.

| Target | What it proves | Images |
| --- | --- | --- |
| `make e2e-kind` | Current source works end to end | Built locally |
| `make e2e-release RELEASE_VERSION=<v>` | Published artifacts work | Pulled from Docker Hub; chart from GHCR |

## `make e2e-kind`

1. Builds `kavryctl`, the Gateway and Operator images, and a sample MCP server.
2. Installs `charts/kavrynt` into `kavrynt-system` with the production profile
   (`examples/helm/values-production.yaml`: three Gateway and two Operator
   replicas) and both NetworkPolicies on (the sample server's pods carry
   `kavrynt.io/gateway-only: "true"`; only `kavrynt-e2e` may call the
   Gateway), and confirms the chart
   installed the `MCPServer` CRD of `config/crd/bases`, owned by Helm with
   the keep policy.
3. Applies `test/e2e/manifests/mcp-server.yaml` and waits for `Ready`.
4. Confirms the CRD rejects an endpoint with embedded credentials.
5. Confirms Gateway RBAC: `get/list/watch` on `MCPServer` only; no writes, no
   Secrets.
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
   `/v1/traffic` without its arguments, runs the console browser smoke test
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
    MCP client authentication: deploys the e2e image's test authorization
    server, turns `gateway.auth` on, and checks the `401` challenge and the
    route's Protected Resource Metadata, that a valid token is accepted and
    not forwarded, that tokens for another route, without the required scope,
    or in the query string are refused, that refusals are recorded, and that
    no token appears in metrics or logs; then turns it off again.
    Then deletes the `MCPServer` and confirms the route disappears.
11. Upgrade and rollback: `helm uninstall` keeps the CRD and `MCPServer`
    resources; the previous release (`UPGRADE_FROM_VERSION`, chart from
    `UPGRADE_FROM_CHART`, images from Docker Hub) is installed on the clean
    cluster with a server; `helm upgrade` to the local build fails before
    touching workloads until the CRD is adopted, then succeeds with the
    server `Ready` and routed; `helm rollback` keeps the CRD, the server, and
    its route. Needs network access to the registries.

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
