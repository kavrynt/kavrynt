# CLAUDE.md

Kavrynt runtime: the Kubernetes-native MCP Control Plane that customers install
in their own clusters. One Go module, one version, one Helm chart
([ADR-0001](docs/ADR-0001-Runtime-Monorepo.md)).

This repository is **public** on GitHub and licensed under the **Apache
License 2.0** ([ADR-0004](docs/ADR-0004-Apache-2.0-License.md)); releases up to
`0.0.4-beta.1` remain under the Elastic License 2.0. Everything committed
here, including this file, is publicly readable: keep internal infrastructure,
cloud account details, pricing, and customer names out of it.

## Repository Ownership

| Path | Owns |
| --- | --- |
| `cmd/kavryctl`, `internal/kavryctl` | Developer and platform CLI (`cli`, `manifest`) |
| `cmd/gateway`, `internal/gateway` | MCP routing (`api`, `kube`, `model`, `routing`), web console (`console`), and (future) enforcement |
| `cmd/operator`, `internal/operator`, `api/v1alpha1`, `config/` | `MCPServer` CRD and reconciliation |
| `charts/kavrynt` | Umbrella Helm chart; subcharts vendored in `charts/kavrynt/charts/{gateway,k8s-operator}` |
| `build/Dockerfile` | All runtime images (`--target gateway`, `--target operator`) |
| `test/e2e`, `scripts/` | Kind end-to-end tests, installers, release contract |
| `examples/kavryctl` | Sample manifests; CI validates every file here with `kavryctl validate` |
| `docs/` | ADRs, `RELEASE.md`, `E2E.md`, per-component docs in `docs/components/` |
| `specs/` | Feature specifications with requirement IDs ([process](specs/README.md)) |

Related repositories: the hosted control plane is `kavrynt-cloud`; cloud
infrastructure is `kavrynt-platform`; the end-to-end lab is `kavrynt-labs`;
customer docs are `product-docs`. The former `kavryctl`, `registry`, `gateway`,
and `operator` repositories are archived and must not receive new changes.

## Architecture Decisions In Force

- [ADR-0001](docs/ADR-0001-Runtime-Monorepo.md): one module, one version, one chart.
- [ADR-0002](docs/ADR-0002-Remove-In-Cluster-Registry.md): the `MCPServer` API
  replaces the in-cluster Registry (removed in `0.0.2-beta.1`).
- [ADR-0003](docs/ADR-0003-Runtime-Authorization.md): Gateway authorization
  design. Implemented: no token passthrough, MCP client authentication
  (SPEC-0006, on by default in the chart per SPEC-0014), endpoint scope
  (SPEC-0029), static upstream
  credentials (SPEC-0011), tool authorization policy (SPEC-0012, opt-in),
  audit events (SPEC-0013, opt-in), their delivery to Kavrynt Cloud
  (SPEC-0017, opt-in; the Cloud ingestion endpoint lives in kavrynt-cloud),
  and token exchange (SPEC-0015). Not yet: approvals.
- [ADR-0004](docs/ADR-0004-Apache-2.0-License.md): the runtime is licensed
  under Apache-2.0 from the release after `0.0.4-beta.1`.
- Identity planes and the system threat model live in `kavrynt-cloud`
  (ADR-0006, THREAT-0001).

## Spec-Driven Development

User-visible changes (the `MCPServer` API, Gateway endpoints and security
behavior, the console, `kavryctl` commands, Helm values, the Cloud connector)
start from a spec in `specs/NNNN-name/` ([process](specs/README.md)).

- Before implementing, read the spec and `plan.md`. If the change has no
  spec and needs one, write `spec.md` with status `Draft` and stop for
  review; do not implement a `Draft` spec.
- Every test that verifies a requirement cites its ID in a comment
  (`// Verifies SPEC-0001-R03.`). Never renumber requirements.
- Set a spec to `Implemented` only when all its requirements are tested;
  `make spec-check` enforces this. Only then may docs describe the feature.
- Bug fixes, refactors, dependency bumps, and docs need no spec.

## What Works Today (do not overstate)

- `MCPServer` registration through Kubernetes or `kavryctl`; the Operator sets
  `Accepted` and `Ready`.
- `kavryctl upgrade` and `kavryctl rollback` (SPEC-0019) run the `helm`
  binary with checks before (release, chart version, CRD ownership;
  `--adopt-crd`) and after (every server routed before is routed again).
- The Operator lists the tools of `Ready` servers (MCP `tools/list`, bounded)
  into `status.tools`, with their MCP behavior hints, and sets a
  `ToolsDiscovered` condition that never affects `Ready`, and records the reported `serverInfo` name and version in
  `status.serverInfo`. A failed discovery shows in `kubectl get`'s
  `Discovery` column and `kavryctl list`'s `TOOLS`, with a message that
  names the cause and never quotes the server or a resolved address
  (SPEC-0027).
- Opt-in Operator connector (`cloud.enabled`) sends full inventory snapshots
  (servers and tools) to Kavrynt Cloud `POST /v1/sync/mcpservers` with a
  cluster token from a Secret. Traffic rollups are not uploaded yet.
- Gateway watches `Ready` servers and proxies **HTTP only** at
  `/mcp/<namespace>.<name>`. `stdio` manifests validate but are not proxied.
  Only `POST`, `GET`, and `DELETE` and the route's own path are forwarded,
  sub-paths only with `spec.forwardSubpaths` (SPEC-0029).
- Endpoint scope (SPEC-0029): an endpoint that reaches another namespace
  (Service names, short names, and, with `global.endpoints.clusterCIDRs`,
  cluster addresses) is not `Ready` (`EndpointNotAllowed`) unless
  `global.endpoints.crossNamespace` allows the pair; a token exchange
  audience not in `global.tokenExchangeAudiences` for the namespace is not
  `Ready` (`AudienceNotAllowed`) and the Gateway exchanges nothing for it.
- `MCPServer.spec.sessionAffinity: MCPSession` (SPEC-0026): for an `http`
  Service endpoint, the Gateway sends each MCP session to the pod that
  started it (a pod tag in front of `Mcp-Session-Id`, from read-only
  EndpointSlices, no shared state), answers `404` for sessions of pods that
  are gone, and the Operator discovers from one pod. Lost sessions are
  `session_not_found` for every server.
- Gateway strips `Authorization`, `Cookie`, and `Proxy-Authorization` before
  forwarding and drops upstream `Set-Cookie`, `Server`, and `X-Powered-By`.
- Gateway and Operator discovery refuse link-local, cloud metadata, and
  loopback addresses at dial time (`internal/egress`); the Gateway passes
  redirects to the client, rejects `..` path segments, cross-origin browser
  requests (`--allowed-origins`), and Gateway loops, and sandboxes proxied
  responses with CSP.
- Opt-in alerting rules (`metrics.prometheusRule`) for the Gateway, the
  Operator, MCP servers, and their tools (SPEC-0021: tool errors, failing
  discovery), each with a runbook in `docs/runbooks` and labelled
  `kavrynt_release`; opt-in notifications (`metrics.notifications`,
  SPEC-0022) render an `AlertmanagerConfig` for Slack, email, Teams, or a
  webhook, checked with `amtool`; the Operator exports
  `kavrynt_operator_mcpserver_ready` and
  `kavrynt_operator_mcpserver_tools_discovered` on a metrics Service.
- Opt-in Grafana dashboards (`gateway.metrics.dashboard`, SPEC-0023): MCP
  traffic, one MCP server, and the team's own from `extra`, all checked by
  `test/dashboards` against the exported metrics and the metrics reference
  in the component docs.
- Opt-in chart NetworkPolicies (`networkPolicy`) make labelled MCP server
  pods reachable only from the Gateway and the Operator, and can limit who
  calls the Gateway.
- Read-only web console at `/ui/` on the Gateway (overview, servers, tools,
  playground, traffic, alerts, dashboard) backed by `GET /v1/servers`,
  `GET /v1/traffic`, `GET /v1/alerts`, and `GET /v1/history`.
  A single `admin` login (on by default; password generated by the chart
  into a mounted Secret) protects the console and those endpoints only.
  It is not client authentication. Failed logins are limited per client,
  behind `gateway.console.auth.trustedProxies` per `X-Forwarded-For`
  client (SPEC-0030).
- Console alerts (SPEC-0020): the Gateway evaluates five MCP conditions
  (server not Ready, discovery failing, upstream errors, tool errors, slow
  route) with the `metrics.prometheusRule` thresholds, from a ConfigMap the
  umbrella chart writes, and serves them at `GET /v1/alerts` behind the
  console login for the console's Alerts page. Traffic conditions are per
  replica.
- Console dashboards (SPEC-0024): each Gateway replica keeps its own calls
  of the last 24 hours in memory (per route and tool, capped by
  `gateway.console.history.maxTools`, lost on restart) and serves them at
  `GET /v1/history` for the Dashboard page and each server's Dashboard tab,
  with the console alerts of the range marked; `gateway.console.grafanaURL`
  links the Grafana dashboards for cluster-wide history.
- MCP client authentication (`gateway.auth`, SPEC-0006): the Gateway is an
  OAuth 2.1 resource server; every `/mcp/` request needs a bearer JWT from a
  trusted issuer whose audience is the route, and Protected Resource
  Metadata is served per route. On by default in the chart (SPEC-0014): an
  install without `resourceURL` and `issuers` fails unless it sets
  `gateway.auth.enabled=false`, which leaves `/mcp/` routes open and is
  warned about at start and in the console.
- `MCPServer.spec.credential` (SPEC-0011): the Gateway and Operator send a
  server's credential from the kubelet-mounted upstream credentials Secret
  (`global.upstreamCredentials.secretName`, keys `<ns>.<name>` and
  `<ns>.<name>.origin`), only to the pinned endpoint origin; neither has
  Secret RBAC.
- Opt-in tool authorization policy (`gateway.policy`, SPEC-0012): OPA embedded
  in the Gateway evaluates `data.kavrynt.authz.decision` from a mounted
  ConfigMap before forwarding; anything but `{"allow": true}` denies. With
  `gateway.policy.filterToolsList` (SPEC-0016), `tools/list` answers list
  only the tools the policy lets that caller call, failing closed to an
  empty list. The input carries the called tool's recorded MCP behavior
  hints (`tool_annotations`, self-reported) and `tool_discovered`
  (SPEC-0028).
- Opt-in audit events (`gateway.audit`, SPEC-0013): one JSON line per `/mcp/`
  request on the Gateway's stdout (`"type":"kavrynt.audit.v1"`), SHA-256
  chained per instance, verified by `kavryctl audit verify`; never arguments,
  results, or tokens. `X-Request-Id` on every `/mcp/` response.
- `MCPServer.spec.tokenExchange` (SPEC-0015): the Gateway exchanges the
  caller's verified token at its issuer (RFC 8693, `client_secret_basic`
  with a client secret from the kubelet-mounted
  `gateway.auth.tokenExchangeSecret`) for one with the server's audience,
  sends only that, and caches it until shortly before expiry; failures
  answer `502`/`503` (`exchange_failed`). The Operator skips tool discovery
  for such servers (`CallerTokenRequired`).
- Opt-in audit delivery (`gateway.audit.cloud`, SPEC-0017): the Gateway also
  sends written audit events to Kavrynt Cloud `POST /v1/sync/audit-events`
  in batches (500 events, 1 MiB, every 10 s) with the cluster token from a
  mounted Secret, retries with backoff, buffers 100,000 events (oldest
  dropped and counted), and alerts with `KavryntAuditDeliveryStale`. The
  Cloud side of that endpoint is not in this repository.
- Not implemented: approvals.
  Never claim them in code comments, docs, or release notes until they are
  implemented and tested.

## Product Boundary

- Trial users consume approved alpha or beta images, `kavryctl` binaries, Helm
  charts, and runbooks.
- Customer-facing docs (`product-docs`) may link to this repository. Ask the
  owner before adding source links to the marketing website.
- Hosted features (cross-cluster inventory, enterprise identity, billing) belong
  in `kavrynt-cloud`, not here.

## Branching

GitFlow:

- `develop`: integration and the default branch.
- `main`: validated release baseline. Release tags are cut from `main` only.
- `feature/<short-kebab-case-name>`: one logical change, from `develop`, merged
  back after `make qa` and `make e2e-kind` pass.
- `release/<version>`: release stabilization when required.
- `hotfix/<short-kebab-case-name>`: urgent fixes.

Never force-push shared branches. Inspect `git status` before editing and
preserve the owner's uncommitted changes. Commit, push, merge, tag, or publish
only when the owner explicitly asks.

Merging, when the owner asks for it:

- Merge pull requests into `develop` with a merge commit
  (`gh pr merge <n> --merge --delete-branch`), only after every CI check on
  that pull request has passed. `develop` has no branch protection, so check
  `gh pr checks <n>` yourself.
- Merge stacked pull requests from the bottom up. Every pull request in a
  stack gets the full checks against its own base, so wait until all of
  them pass, then merge them in order. Before merging one, retarget the pull
  request stacked on it to `develop` (`gh pr edit <next> --base develop`):
  GitHub closes, not retargets, a pull request whose base branch is
  deleted. While nothing else lands on `develop` in between, its tested
  head merges unchanged and needs no new run; otherwise rerun its checks
  first (`gh pr close` then `gh pr reopen`).
- After merging, delete the feature branch on the remote and locally
  (`git branch -d`), switch to `develop`, and pull. Never delete `develop`,
  `main`, or `release/*` branches.

## Commands

```bash
make help            # list targets
make qa              # release contract, spec check, gofmt, race tests, console tests, vet, staticcheck, gosec, govulncheck, helm lint/template/unittest, alert tests
make test            # go test -race ./...
make console-test    # web console unit tests (node --test, no npm packages)
make spec-check      # specs well formed; Implemented requirements cited by tests
make spec-new NAME=x # scaffold specs/NNNN-x/spec.md
make helm-plugins    # once: install the pinned helm-unittest plugin
make helm-unittest   # chart unit tests (charts/kavrynt/tests)
make alerts-test     # alert rules: runbook per alert, promtool tests; notifications: amtool (needs Docker)
make build           # bin/kavryctl, bin/gateway, bin/operator
make docker-build    # local images tagged $(LOCAL_VERSION)
make e2e-kind        # disposable Kind cluster, full product loop (scripts/e2e-kind.sh)
make e2e-release RELEASE_VERSION=<v>   # validate published chart and images
```

Run one package's tests: `go test -race ./internal/gateway/...`.
Toolchain: Go 1.26+, Node 22+ (console tests only), Docker, Helm 3 or 4 with the helm-unittest plugin,
kubectl, Kind. Chart values are validated by hand-written
`values.schema.json` files that reject unknown keys: add every new value to
the schema and cover template changes in `charts/kavrynt/tests/`.

## CI and Release

- `.github/workflows/qa.yml` (pull requests into `develop`, `main`, and
  `feature/**`, so stacked pull requests are tested too; pushes to `develop`
  and `main`; manual runs): Go QA and SAST, Trivy source scan, Helm and
  release contract, image build with Trivy (amd64 on pull requests; amd64
  and arm64 on `develop` and `main`). Kind e2e runs on pull requests and
  manual runs, in parallel with Go QA. Pull requests that change only
  Markdown, `specs/`, or `docs/` (not Go files) skip the images and the e2e.
- `.github/workflows/codeql.yml` (same pull requests and pushes, weekly):
  CodeQL for Go, the console's JavaScript, and the workflows, reported to
  code scanning. `scorecard.yml` (pushes to `develop`, weekly) reports
  OpenSSF Scorecard there too.
- `.github/workflows/release.yml` (tag `v*`): tag must match
  `X.Y.Z-(alpha|beta)[.N]`, be on `main`, and equal the chart version. Publishes
  to GHCR (canonical), mirrors to Docker Hub, Cosign-signs both, builds
  `kavryctl` for six platforms, pushes the OCI chart, then runs `e2e-release`.
- Full procedure, verification, and failure handling: [docs/RELEASE.md](docs/RELEASE.md).
  Never overwrite a published tag; fix forward with the next prerelease number.

## Bumping The Version

`scripts/verify-release-contract.sh` (part of `make qa`) fails unless one
version matches in all of these places:

- `charts/kavrynt/Chart.yaml` `version` and `appVersion`
- `charts/kavrynt/charts/gateway/Chart.yaml` and `charts/kavrynt/charts/k8s-operator/Chart.yaml` (`version`, `appVersion`)
- both `tag:` lines in `charts/kavrynt/values.yaml`
- the `artifacthub.io/images` tags in `charts/kavrynt/Chart.yaml`, and
  `artifacthub.io/prerelease` (`"true"` for any `-alpha`/`-beta` version)
- the operator image in `config/manager/deployment.yaml`
- `v<version>` in `scripts/install.sh` and `scripts/install.ps1`

Versions look like `0.0.2-beta.1`. No mutable tags such as `latest`.

## Engineering Standards

- Keep components separated: Gateway contains no reconciliation logic; the
  Operator never proxies traffic (tool discovery is its own MCP client
  session); `kavryctl` imports no server internals other than shared API
  types. Limits shared by the Gateway and the Operator live in
  `internal/mcpmeta`; their egress policy for MCP endpoints lives in
  `internal/egress`.
- The console (`internal/gateway/console/static`) is plain HTML, CSS, and
  JavaScript embedded in the Gateway: no build step, no external assets.
  Insert untrusted data with `textContent` only, never `innerHTML`. Use
  classic scripts, not ES modules: browsers send `Origin` on module script
  requests and the Gateway rejects origins outside `--allowed-origins`.
  Keep logic in the DOM-free files (`stats.js`, `state.js`, `format.js`) so
  `make console-test` covers it.
- Shared Kubernetes API types live only in `api/v1alpha1`. The CRD schema is
  hand-written (no controller-gen) and exists twice, kept identical:
  `config/crd/bases/kavrynt.io_mcpservers.yaml` and
  `charts/kavrynt/charts/k8s-operator/files/kavrynt.io_mcpservers.yaml` (the
  chart renders it as a template, so `helm upgrade` updates it). Change
  both with the Go types, and update RBAC in `config/rbac` and the chart
  together.
- Prefer simple, dependency-light Go. New dependencies must pass govulncheck
  and Trivy.
- Treat Kubernetes manifests, MCP payloads, tool metadata, and Gateway traffic
  as untrusted input.
- Tests run without network access: use fake `http.RoundTripper`s and
  `t.TempDir()`.
- Use explicit HTTP status codes and stable JSON responses.

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials, and
  never log secret values.
- Images: non-root, distroless, base images pinned by digest, OCI labels.
- Least-privilege RBAC: Gateway is read-only on `MCPServer` with no Secret
  access; disable service account token automount unless needed;
  `readOnlyRootFilesystem: true` unless a write path is required.
- Pin GitHub Actions by commit SHA.
- Images are published only by `release.yml` after every gate passes.

## End-To-End Contract

`make e2e-kind` must verify:

1. Gateway and Operator become ready.
2. Applying an `MCPServer` produces a `Ready` condition.
3. The CRD rejects unsafe endpoints at admission.
4. Gateway RBAC is read-only on `MCPServer` and EndpointSlices and has no
   Secret access.
5. The Operator records discovered tools in `status.tools`; a failed
   discovery leaves the server `Ready`.
6. Gateway and `kavryctl list` expose the expected route.
7. A JSON-RPC `tools/list` call succeeds through Gateway.
8. `kavryctl register`, `inspect`, and `unregister` work against the cluster.
9. Deleting an `MCPServer` removes its Gateway route.
10. The Gateway rejects cross-origin browser requests and refuses to proxy to
    cloud metadata addresses; `/v1/servers` and `/v1/traffic` answer `401`
    until a login with the generated admin password; the console,
    `/v1/servers`, and `/v1/traffic` work and never expose tool arguments;
    an unreachable server appears on `/v1/alerts` as `DiscoveryFailing`
    and disappears once deleted; a tool call appears on `/v1/history` for
    its route and tool; the console browser smoke test, including the
    dashboards at 360 and 1280 px, passes (always in CI).
11. With the production profile, a Gateway rolling restart under load fails
    no MCP call, and a standby Operator takes over from a deleted leader.
    With the chart's NetworkPolicies on, a direct call to a labelled MCP
    server and a call from a namespace the Gateway policy does not allow
    both fail, while calls through the Gateway succeed.
    A stateful three-replica server with session affinity keeps ten
    agents' sessions through the Gateway replicas; without it, sessions
    are lost as `session_not_found`.
    A server that needs its own token is routed and discovered with the
    stored upstream credential; an unstored one answers `503`; the value
    appears in no response, status, metric, or log.
    With MCP client authentication on (test authorization server in the e2e
    image), calls without a token get `401` pointing at the route's metadata,
    a valid token works and never reaches the MCP server, and wrong
    audiences, missing scopes, and query tokens are refused. A server that
    declares token exchange receives the caller's exchanged token for its
    own audience, never the caller's. A policy from a
    ConfigMap allows one client's tool call and denies another's, each
    client's `tools/list` shows only the tools it may call, and a broken
    policy update leaves those decisions in force. Audit events record
    the allowed, denied, and unauthenticated calls without arguments, and
    `kavryctl audit verify` accepts every Gateway's chain.
12. An install with default values is refused until it names an
    authorization server or opts out (`gateway.auth.enabled=false`).
    `helm uninstall` keeps the CRD and `MCPServer` resources. An upgrade from
    `0.0.5-beta.1` is refused without a `gateway.auth` choice, fails cleanly
    until the CRD is adopted, then keeps servers `Ready` and routed. From the
    previous published release, `kavryctl upgrade` reports them routed and
    `kavryctl rollback` keeps them.

Keep the Kind workflow disposable and isolated from the user's current
Kubernetes context. On macOS and Apple Silicon, build images locally and load
them into Kind. Prefer port-forwarding over NodePort. Never modify or delete
Kind clusters this workflow did not create. Details: [docs/E2E.md](docs/E2E.md).
