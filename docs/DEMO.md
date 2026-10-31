# Demo

A repeatable 10 to 12 minute live demo of the Kavrynt runtime on a disposable
Kind cluster, written for a CNCF TAG or TOC presentation. It shows only what
works today and states what does not.

## What the audience sees

1. One Helm chart installs an Operator, a Gateway, and the `MCPServer` CRD.
2. Two teams own two MCP servers (`team-sre/incidents`, `team-platform/runbooks`)
   that run as ordinary Deployments. Kavrynt does not run them.
3. One server is registered with `kubectl apply`, the other with `kavryctl`.
4. The Operator marks them `Ready` and writes their tools into `status.tools`.
5. Tool calls go through one Gateway URL per server, `/mcp/<namespace>.<name>`,
   and appear in the console and in Prometheus metrics, without arguments.
6. Guardrails: admission rejects credentials in endpoints, the Gateway strips
   caller credentials, refuses cross-origin browser requests and cloud
   metadata addresses, and its RBAC is read-only with no Secret access.
7. The caveat, live: an unauthenticated client acknowledges an incident, and
   nothing stops it.
8. Unregistering a server removes its route.

The demo MCP servers are in [examples/demo](../examples/demo). Their data is
fictional and kept in memory.

## Setup

Requirements: Docker, Kind, kubectl, Helm, Go, curl, and jq.

```bash
make demo-up            # about 5 minutes the first time; reruns reuse the cluster
make demo-walkthrough   # Enter advances each step
make demo-down          # delete the cluster
```

- The cluster is `kavrynt-demo`, with its own kubeconfig at
  `.kavrynt/demo/kubeconfig`. Your current Kubernetes context is not used or
  changed. To run your own commands against it:
  `export KUBECONFIG=$PWD/.kavrynt/demo/kubeconfig PATH=$PWD/.kavrynt/demo/bin:$PATH`.
- `make demo-down` deletes only a cluster that `make demo-up` created.
- MCP client authentication is on by default, but the demo has no
  authorization server, so `make demo-up` installs with the explicit
  opt-out, `--set gateway.auth.enabled=false`, and the Gateway logs that MCP
  routes are unauthenticated. An install with your own authorization server
  sets `gateway.auth.resourceURL` and `gateway.auth.issuers` instead; see the
  [chart README](../charts/kavrynt/README.md#authenticating-mcp-clients).
- Images are built from your checkout and loaded into Kind. Nothing is pulled
  from a registry at demo time.
- The walkthrough serves the Gateway on `http://127.0.0.1:18080` for as long
  as it runs (set `DEMO_PORT` to change it), and resets the demo before it
  starts: it deletes all registrations and restarts the demo servers.
- `make demo-forward` serves the Gateway without the walkthrough.
- `DEMO_AUTO=1 make demo-walkthrough` runs every step unattended and fails if
  any output differs from what the talk track expects. Use it to rehearse.

## Before the presentation

- [ ] The day before, on the presentation laptop: `make demo-up`, then
      `DEMO_AUTO=1 make demo-walkthrough`. After that, the demo needs no
      network access.
- [ ] Record one full run (screen and audio) as a fallback.
- [ ] Use a large terminal font, and a window at least 120 columns wide.
- [ ] Open one terminal at the repository root and one browser tab. The
      console tab works once the walkthrough has started; sign in as `admin`
      with the password step 1 prints.
- [ ] Decide whether to connect a real agent (see below), and test it on the
      same network.
- [ ] Confirm `1.0.0-beta.3` is published (`gh release view v1.0.0-beta.3`)
      and its `make e2e-release` run passed. See
      [Questions to prepare for](#questions-to-prepare-for).

## Run of show

| Step | Time | Say |
| --- | --- | --- |
| Intro | 0:30 | MCP servers multiply across teams. Platform teams need to know where they are, route to them consistently, and eventually control who calls what. Kavrynt treats MCP servers as Kubernetes resources. |
| 1. Control plane | 0:45 | One chart. The Operator reconciles; the Gateway only proxies and is read-only on the API. Like Argo CD, the chart generated a console admin password; sign in to the console with it. |
| 2. Two teams | 0:45 | These are ordinary Deployments owned by two teams. No routes yet. Show the empty console. |
| 3. kubectl | 1:00 | `MCPServer` is the API, so GitOps, RBAC, and admission work as they do for any resource. |
| 4. kavryctl | 0:45 | The same API for developers. Routes are namespace-qualified, so names never collide across teams. |
| 5. Discovery | 1:00 | The Operator ran MCP `initialize` and `tools/list` and recorded the tools in status. A failed discovery never affects `Ready`. Refresh the console. |
| 6. Agent calls | 1:30 | One URL per server. A tool error is reported as a tool error, not a transport failure. |
| 7. Live traffic | 1:00 | Method, tool, outcome, and latency per route and per tool, in the console and in Prometheus. Arguments are never recorded. Open Overview: the failing tool is listed. |
| 8. Guardrails | 2:00 | What holds even with authentication turned off, as in this demo: admission validation, credential stripping, origin checks, egress refusal, least-privilege RBAC. |
| 9. The caveat | 0:45 | This call changed state, and nothing checked who made it, because the demo opted out of client authentication. With it on (the default), the Gateway needs a token for the route; the opt-in policy decides per tool (ADR-0003). Approvals are next. |
| 10. Lifecycle | 0:30 | Delete the resource and the route goes away. |
| 11. Not yet | 1:00 | Read the list in the next section verbatim. |

## What Kavrynt does not do yet

Show this list as a slide as well. Do not soften it.

- MCP client authentication is on by default, but this demo opts out (and
  token exchange needs it): without authentication, anyone who can reach
  the Gateway can call every routed tool. The console has an admin login,
  but it does not protect MCP routes.
- Tool authorization policy and audit events exist but are off by default;
  there are no approvals.
- HTTP MCP servers only. `stdio` manifests validate but are not proxied.
- Kavrynt does not deploy MCP servers; it registers and routes to existing
  endpoints.
- The demo runs one replica. A high-availability profile and upgrades are
  tested in CI; there is no backup or restore workflow yet.
- The demo uses the console's shared `admin` login; sign-in through an
  identity provider is available but not shown. There are no roles.
- Beta (`1.0.0-beta.N`), one maintainer organization, no named adopters yet.
- Run it in trusted development or evaluation clusters only.

## Connect a real agent (optional)

Any MCP client that speaks Streamable HTTP can use a Gateway route while the
walkthrough runs, for example after step 6:

```bash
# MCP Inspector, from a scratch directory
npx @modelcontextprotocol/inspector --cli \
  http://127.0.0.1:18080/mcp/team-sre.incidents --transport http --method tools/list

# Claude Code
claude mcp add --transport http incidents http://127.0.0.1:18080/mcp/team-sre.incidents
```

SDK clients also open the optional `GET` event stream, which the demo servers
do not offer. The Gateway records that request as `http_get` with outcome
`ok`, since `405` is the answer the MCP specification gives for it, so it
does not raise the console's error rate.

## Questions to prepare for

- **Licensing.** The runtime is Apache-2.0 from `0.0.5-beta.1`, the first
  release after `0.0.4-beta.1` ([ADR-0004](ADR-0004-Apache-2.0-License.md)).
  Releases up to and including `0.0.4-beta.1` remain under the Elastic
  License 2.0.
- **Why a CRD and an Operator?** Registration lives in the Kubernetes API
  ([ADR-0002](ADR-0002-Remove-In-Cluster-Registry.md)), so namespaces, RBAC,
  GitOps, and admission apply without a separate registry service.
- **Authentication and authorization.** Designed in
  [ADR-0003](ADR-0003-Runtime-Authorization.md). Client authentication (OAuth
  2.1 bearer tokens, per-route audience) is implemented and on by default;
  policy and audit are opt-in; approvals are not built. The Gateway never forwards caller credentials
  upstream.
- **Multi-tenancy.** Routes are namespace-qualified, and
  `global.watchNamespaces` limits the Gateway to listed namespaces with
  per-namespace Roles. There is no per-tenant policy yet.
- **Relationship to the hosted product.** The Operator can send inventory to
  Kavrynt Cloud, but only when `cloud.enabled` is set; it is off by default.
  Everything in this demo runs without it.
- **Governance and maturity.** See [GOVERNANCE.md](../GOVERNANCE.md),
  [MAINTAINERS.md](../MAINTAINERS.md), and [ROADMAP.md](../ROADMAP.md). The
  roadmap applies for Sandbox once there are maintainers outside one
  organization and adopters; client authentication has shipped.
- **Supply chain.** Images are distroless and non-root, built with a Go
  toolchain pinned by digest, and scanned by govulncheck and Trivy in CI.
  Release images and checksums are Cosign-signed by the release workflow
  ([RELEASE.md](RELEASE.md)). `0.0.5-beta.1` builds with Go 1.26.10, which
  fixes GO-2026-6809 (`crypto/x509`), present in the `0.0.4-beta.1` images
  built with Go 1.26.9.

## If something goes wrong

| Symptom | Fix |
| --- | --- |
| `port-forward on 18080 failed` | Another process uses the port: `DEMO_PORT=18081 make demo-walkthrough`. |
| A step prints `error: expected output ...` | Re-run `make demo-walkthrough`; it resets state first. |
| `no demo cluster` | `make demo-up`. |
| Cluster is broken | Switch to the recording, then run `make demo-down && make demo-up` (about 5 minutes). |
| Docker Desktop restarted | `make demo-up` reuses the cluster once Docker is back. |
