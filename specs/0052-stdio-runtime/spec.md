# SPEC-0052: Run stdio MCP servers behind the Gateway

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-11-27 |
| Issue | [#176](https://github.com/kavrynt/kavrynt/issues/176) |
| ADRs | [ADR-0001](../../docs/ADR-0001-Runtime-Monorepo.md), [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

A `stdio` MCP server can be registered with an image, and the Operator runs
it for the team: it starts the server's command in the team's namespace,
behind a small Kavrynt adapter that speaks MCP Streamable HTTP, and routes
the server like any `http` server. Policy, audit, metrics, client
authentication, and the console then work for it unchanged. Each MCP session
gets its own server process. The feature is off unless the platform team
enables it, and the platform team limits which namespaces and images it will
run.

## Problem

Many published MCP servers speak only stdio. Today `transport: stdio`
validates but is never routed (`Ready=False`, reason `UnsupportedTransport`),
and the Gateway answers `404`. A team must build its own Streamable HTTP
bridge image, write the Deployment and Service, and register the bridge as an
`http` server (see `examples/stdio-bridge`). That is work most teams expect
the platform to do, and each hand-made bridge decides on its own how
sessions, process lifetime, resource limits, and pod hardening work.

A stdio server is built for exactly one client: its process holds that
client's state. A bridge that shares one process among all callers mixes
their state. The Gateway authenticates callers and binds sessions to them
(SPEC-0042), so the runtime must keep that boundary below the Gateway too.

The Gateway stays a proxy: it never starts processes (see Non-goals).
Evidence: [#176](https://github.com/kavrynt/kavrynt/issues/176).

## Goals

- A team registers a stdio server by naming its image and command, and the
  server becomes `Ready` and routed at `/mcp/<namespace>.<name>`.
- Each MCP session talks to its own server process, so one caller's state
  never reaches another.
- A platform team decides whether the feature exists, where it runs, and
  which images it may run, because an `MCPServer` author could otherwise run
  any image in the cluster.
- The workload is hardened and bounded by default.
- Everything that works for `http` servers (policy, audit, client
  authentication, session affinity, alerts, console) works for these.

## Non-goals

- Running stdio processes inside the Gateway or inside the Operator.
  Neither gains the ability to execute server code.
- Building, scanning, or signing the team's server image.
- Servers that need the caller's own token (`tokenExchange`) or a stored
  upstream credential (`credential`). Both stay `http`-only. A server's own
  secrets come in through `runtime.secretEnv` (R09).
- Autoscaling, persistent volumes, and containers beyond the adapter.
- Replacing hand-made bridges: an `http` server stays valid and unchanged.
- Cross-replica session recovery: a session lives and dies with its pod, as
  for `sessionAffinity` (SPEC-0026).

## Requirements

### API and validation

- **SPEC-0052-R01** `MCPServer.spec.runtime` MUST exist, with `image`
  (required), `replicas` (default 1), `maxSessions` per pod (default 10),
  `idleTimeout` (default 15 minutes), `resources`, and `secretEnv` (R09).
  It MUST be valid only with `spec.transport: stdio`; the CRD (both
  copies) and `Validate` MUST reject it with `http`.
- **SPEC-0052-R02** A `stdio` server without `spec.runtime` MUST keep
  today's behavior: `Accepted=True`, `Ready=False` (`UnsupportedTransport`),
  no workload, no route, and the existing `kavryctl` warning. Existing
  manifests MUST NOT change meaning.
- **SPEC-0052-R03** `spec.runtime.image` MUST be an image reference with a
  digest (`name@sha256:...`) unless the platform allows tags (R05). Values
  MUST be bounded in length, and `spec.runtime` MUST NOT accept host
  mounts, privileged settings, host networking, service accounts, or any
  other pod field.
- **SPEC-0052-R04** `spec.credential`, `spec.tokenExchange`,
  `spec.endpoint`, and `spec.forwardSubpaths` MUST be rejected with a
  `spec.runtime` (the endpoint is generated). `spec.sessionAffinity` MUST
  behave as `MCPSession` and need not be set.

### Platform control

- **SPEC-0052-R05** The feature MUST be off unless
  `operator.stdioRuntime.enabled=true`. When on, the Operator MUST create
  workloads only in the namespaces listed in
  `operator.stdioRuntime.namespaces` and only for images whose registry
  prefix is in `operator.stdioRuntime.allowedImages` (both required when
  enabled; a wildcard is never accepted).
  `operator.stdioRuntime.allowTags=true` MAY permit tags.
- **SPEC-0052-R06** A server that asks for a workload the platform does not
  allow (feature off, namespace not listed, image not allowed, tag without
  `allowTags`) MUST be `Accepted=True` and `Ready=False` with a reason that
  names the cause (`StdioRuntimeDisabled`, `NamespaceNotAllowed`,
  `ImageNotAllowed`), and MUST get no workload. The message MUST NOT quote
  the image or any value beyond the reason.
- **SPEC-0052-R07** The Operator MUST gain only `create`, `get`, `list`,
  `watch`, `update`, `patch`, and `delete` on `deployments` and `services`
  in the listed namespaces (a `Role` per namespace, not a `ClusterRole`),
  and MUST gain no access to Secrets, to `pods/exec` or `pods/log`, or to
  service accounts. With the feature off the chart MUST add no such rule.

### The workload

- **SPEC-0052-R08** For an allowed server the Operator MUST create and keep
  one Deployment and one Service in the server's namespace, owned by the
  `MCPServer` (removed when it is deleted), and MUST use that Service as the
  server's endpoint for routing, tool discovery, and session affinity. A
  manual change to the generated objects MUST be reverted.
- **SPEC-0052-R09** `runtime.secretEnv` MUST list environment variables
  taken from named Secret keys of the server's namespace (`secretKeyRef`).
  The Operator MUST NOT read the Secret; the kubelet resolves it. Names
  MUST NOT collide with `spec.environment`, and the values MUST NOT appear
  in the `MCPServer`, its status, logs, or metrics.
- **SPEC-0052-R10** The pod MUST run as non-root, with no service account
  token, a read-only root filesystem, no privilege escalation, all
  capabilities dropped, `RuntimeDefault` seccomp, CPU and memory limits
  (defaults when `resources` is unset), and the label the chart's
  NetworkPolicies select (`kavrynt.io/gateway-only: "true"`). The adapter
  MUST come from a Kavrynt image pinned by digest in the chart.
- **SPEC-0052-R11** The server is `Ready` only when the Deployment has an
  available replica for the current spec. Otherwise it MUST be
  `Ready=False` with reason `WorkloadNotReady`, so the Gateway does not
  route to a server that cannot start.

### The adapter

- **SPEC-0052-R12** The adapter MUST serve MCP Streamable HTTP at `/mcp`
  (`POST`, `GET`, `DELETE`) and MUST start one child process, from
  `spec.command` and `spec.args`, for each session created by `initialize`,
  with the child's standard input and output carrying the JSON-RPC
  messages. Two sessions MUST NOT share a process.
- **SPEC-0052-R13** The child MUST get only the environment declared in
  `spec.environment` and `runtime.secretEnv`, not the adapter's or the
  pod's. Its standard error MUST NOT be sent to clients; it MAY be counted
  by lines and bytes, never logged as content.
- **SPEC-0052-R14** A session MUST end, and its process MUST be killed, on
  `DELETE`, when the process exits, after `idleTimeout` without a request,
  and when the pod stops. A request for an ended session MUST get `404`
  (`session_not_found` at the Gateway). At `maxSessions`, a new
  `initialize` MUST get `503` and MUST NOT start a process.
- **SPEC-0052-R15** The adapter MUST refuse messages above the limits in
  `internal/mcpmeta`, and MUST end a session whose child writes anything
  to standard output that is not a JSON-RPC message.
- **SPEC-0052-R16** Tool discovery MUST work: the Operator's discovery
  session starts a process, lists tools, and ends it. A failed discovery
  MUST leave the server `Ready`, as for any server (SPEC-0027).

### Records and end to end

- **SPEC-0052-R17** These servers MUST appear in `/v1/servers`, the console,
  metrics, and audit events exactly as `http` servers do. `kavryctl doctor`
  MUST report the workload state, and `kavryctl export` MUST include the
  `runtime` block (`secretEnv` as names only, never values).
- **SPEC-0052-R18** In the e2e, a stdio test server MUST be registered with
  the runtime enabled for its namespace. The test MUST show the server
  `Ready`; a `tools/call` through the Gateway answered; two sessions using
  two processes; an ended session answered `404`; a server in an unlisted
  namespace and one with a disallowed image `Ready=False` with no
  workload; and the Operator's RBAC without Secret access.

## Acceptance criteria

- Given the runtime enabled for `team-a` and `ghcr.io/acme/` allowed, when a
  team applies a `stdio` MCPServer with `runtime.image`
  `ghcr.io/acme/files@sha256:...`, then a Deployment and a Service appear,
  the server becomes `Ready`, and an authenticated `tools/call` at
  `/mcp/team-a.files` is answered (R01, R05, R08, R11, R12).
- Given two clients, when each initializes a session, then each session has
  its own process and one client's state is invisible to the other (R12).
- Given the feature is off, when a team applies the same manifest, then it
  is `Ready=False` with `StdioRuntimeDisabled` and nothing is created (R06).
- Given an image outside the allowed prefixes, then `ImageNotAllowed` and no
  workload (R05, R06).
- Given an existing `stdio` manifest with no `runtime`, then nothing changes
  (R02).
- Given a session idle past `idleTimeout`, then its process is gone and the
  session ID answers `404` (R14).
- Given the server deleted, then its Deployment and Service are gone (R08).

## Security and privacy

This is the largest privilege change in the runtime, so it is opt-in and
fenced. Whoever can create an `MCPServer` in an allowed namespace can run an
allowed image there; the platform team bounds that with namespaces and image
prefixes (R05), by digest unless it allows tags (R03). The Operator's added
rights are namespaced and exclude Secrets (R07, R09). The server code is the
team's: Kavrynt does not scan it, and the pod is hardened, bounded, and
reachable only through the Gateway and the Operator (R10). The adapter
handles untrusted bytes from the child and from clients: it bounds
messages, never forwards standard error, and ends a session on malformed
output (R13, R15). Tool arguments, results, and secret values are never
logged or stored. Every failure ends the session or leaves the server not
`Ready`; none opens a path around the Gateway.

## Compatibility

Adds `MCPServer.spec.runtime` (a CRD change in both copies and in
`api/v1alpha1`), Operator RBAC and behavior that exist only when the feature
is enabled, new chart values under `operator.stdioRuntime` with schema
entries, a new adapter image built by `build/Dockerfile`, and new condition
reasons. Off by default. Existing `stdio` and `http` servers are unchanged
(R02). Rolling back to a release without the feature leaves generated
workloads running but unmanaged; the upgrade notes MUST say so. A `kavryctl`
older than this release rejects the new field as unknown.

## Documentation

Gateway and Operator component docs, the chart README (values), a runtime
example beside `examples/stdio-bridge`, `kavryctl` docs, the e2e contract in
`CLAUDE.md`, "What Works Today", and `ROADMAP.md`.

## Open questions

- **Should the Operator create workloads, or should Kavrynt ship a
  documented bridge only?** Recommended: the Operator does, opt-in (this
  spec). It is what makes stdio supported, but it gives the Operator write
  access to Deployments, which it never had. Alternative: ship the adapter
  image and a chart template or `kavryctl` generator and keep the Operator
  read-only. Owner decides.
- **Adapter delivery.** A stdio server cannot be a sidecar: the adapter must
  own the child's standard input and output. Recommended: an init container
  copies a static adapter binary into an `emptyDir` and the server's image
  runs it as the entrypoint, so any image works without a shell. It needs an
  image that can run a copied binary. Alternative: a Kavrynt base image the
  team builds `FROM`. Owner decides.
- **Image policy.** Digest by default plus allowed prefixes (R03, R05), or
  also a Cosign signature check? Recommended: no signature check in the
  first version.
- **Process per session or per pod.** Recommended: per session (R12), at
  the cost of memory and start time. A shared process is cheaper but mixes
  callers' state.
- **Scope of `runtime`.** Should `replicas` and `resources` exist in the
  first version, or a single fixed replica? Recommended: keep them; session
  affinity already handles replicas.
- **Secrets.** Is `secretEnv` (R09) in scope now? Recommended: yes, since
  nearly every stdio server needs a token and the Operator never reads it.
- **Spec number.** Confirm 0052: the open specification pull requests #178
  and #216 both claim 0050, so 0050 and 0051 are left for them.
