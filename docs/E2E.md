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
2. Installs `charts/kavrynt` into `kavrynt-system`.
3. Applies `test/e2e/manifests/mcp-server.yaml` and waits for `Ready`.
4. Confirms the CRD rejects an endpoint with embedded credentials.
5. Confirms Gateway RBAC: `get/list/watch` on `MCPServer` only; no writes, no
   Secrets.
6. Calls `tools/list` and `tools/call` through
   `/mcp/kavrynt-e2e.example-mcp-server` and checks the MCP traffic metrics,
   including that tool arguments never appear in them.
7. Registers, inspects, routes to, and unregisters a second server with
   `kavryctl`.
8. Deletes the `MCPServer` and confirms the route disappears.

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
