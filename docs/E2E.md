# End-to-End Tests

All tests create a disposable Kind cluster, address it with an explicit
`--context kind-<cluster>` on every command, and delete the cluster afterwards
(`KEEP_CLUSTER=1` keeps it for debugging; see [Cleanup](#cleanup)). Kind adds
the cluster to your kubeconfig and selects it while the test runs, and
removes it again at the end. If that cluster was your current context, kubectl
is left with none: see [Cleanup](#cleanup). To keep your own kubeconfig out of
it entirely, point `KUBECONFIG` at a file that does not exist yet:
`KUBECONFIG="$HOME/.kube/kavrynt-e2e.config" make e2e-kind`. Kind writes the
test cluster only there, and a cluster kept with `KEEP_CLUSTER=1` is reached
by exporting the same variable. Delete that file when you are done.
Prerequisites: Docker, Kind, `kubectl`, Helm, `jq`, Go.
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
    not forwarded, that a `GET` event stream ends cleanly by its token's
    expiry plus the clock skew and is recorded as `token_expired`, that a
    session ID is bound to its caller (another caller's token gets `404`) and
    the session keeps working through a Gateway rolling restart, that
    tokens for another route, without the required scope,
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

## Cleanup

What a run removes when it exits, whether it passes or fails (after printing
pods, events, and component logs on failure): the Kind cluster it created,
with its kubeconfig context, the port-forwards, and its temporary directory. A
cluster it did not create is never deleted. The script ignores a failed
`kind delete cluster`, for example when Docker has stopped, so after a failed
run check `kind get clusters`. Delete a cluster that is still there only if
this run created it. If the run stopped with `Kind cluster ... already
exists`, that cluster was there before the run and is not yours to delete:
pick another name with `KIND_CLUSTER_NAME` instead.

What stays on your machine, and how to remove it:

| Left behind | When | Remove with |
| --- | --- | --- |
| The Kind cluster | `KEEP_CLUSTER=1`, or the run was killed (`kill -9`, power loss) before it could clean up | `kind delete cluster --name <cluster>` |
| Local images `kavrynt/{gateway,operator,e2e-mcp-server,demo-mcp-server}:<version>-local`, and `kavrynt/e2e-mcp-server:<version>-verification` from `e2e-release` | Always; the build keeps them for the next run | the `docker rmi` command below |
| A throwaway kubeconfig | If you set `KUBECONFIG` as described above | `rm` the file |
| Docker build cache | Always | `docker builder prune` (optional; the next build is slower) |
| `.kavrynt/` in the checkout | After `make demo-up` | `make demo-down` |

After testing, remove everything the tests created:

```bash
# 1. Clusters. The default names are kavrynt-alpha0 (e2e-kind),
#    kavrynt-release-<version> (e2e-release), and kavrynt-demo (demo).
#    Delete only clusters you created for testing.
kind get clusters
kind delete cluster --name <cluster>     # also removes its kubeconfig context
# If you ran the e2e with a throwaway KUBECONFIG, delete with the same one, or
# Kind also removes a same-named context from your own kubeconfig:
KUBECONFIG="$HOME/.kube/kavrynt-e2e.config" kind delete cluster --name <cluster> \
  && rm -f "$HOME/.kube/kavrynt-e2e.config"   # remove the file only if the delete worked
# The demo cluster has its own kubeconfig: use make, not kind.
make demo-down                           # if you ran make demo-up

# 2. Local test images (only tags ending in -local or -verification)
docker image ls --filter 'reference=kavrynt/*:*-local' \
  --filter 'reference=kavrynt/*:*-verification' --format '{{.Repository}}:{{.Tag}}' \
  | xargs docker rmi

# 3. Verify nothing is left
kind get clusters
docker ps -a --filter 'label=io.x-k8s.kind.cluster'
docker image ls 'kavrynt/*'
```

If the cluster you delete is the one `kubectl` has selected, Kind removes its
context and kubectl is left with no current context: `kubectl config
current-context` prints `error: current-context is not set`, and commands try
`localhost:8080`. Select another cluster with `kubectl config get-contexts`
and `kubectl config use-context <name>`.

A Kind cluster that no longer exists can leave its context in your kubeconfig,
for example after `docker rm` or a Docker reset. `kind delete cluster --name
<cluster>` still removes those entries (context, cluster, and user), even when
the cluster is already gone, so run it for each stale `kind-<cluster>` context
in `kubectl config get-contexts`.

## CI

`.github/workflows/qa.yml` runs `e2e-kind` on pull requests
and manual dispatch. `.github/workflows/release.yml` runs `e2e-release` after
publishing.

## Troubleshooting

- `Kind cluster ... already exists`: delete it or set `KIND_CLUSTER_NAME`.
  The scripts never delete a cluster they did not create.
- `kubectl` says `current-context is not set` or `localhost:8080 was refused`
  when you debug a kept cluster (`KEEP_CLUSTER=1`): the e2e script passes
  `--context kind-<cluster>` on every call, and a deleted cluster can leave
  kubectl with no current context. Run `kubectl --context kind-<cluster> ...`,
  or `kubectl config use-context <name>`; see [Cleanup](#cleanup). The demo
  (`make demo-up`) is different: it uses its own kubeconfig, see
  [DEMO.md](DEMO.md).
- Image pulls time out (`TLS handshake timeout` from Docker Hub): retry; base
  images are pulled during the build.
- On failure, the scripts print pods, events, and component logs before
  deleting the cluster.
