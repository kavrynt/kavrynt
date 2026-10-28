# SPEC-0027: Discovery diagnostics

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-27 |
| Issue | #82 |
| ADRs | [ADR-0002](../../docs/ADR-0002-Remove-In-Cluster-Registry.md) |
| Supersedes | none |

## Summary

When the Operator cannot list a server's tools, `kubectl get mcpserver` and
`kavryctl list` say so, and the `ToolsDiscovered` message names the cause
and the next step. `Ready` keeps its meaning: the Gateway routes the
server. The difference between "routed" and "working" becomes visible
instead of hidden behind `READY True`.

## Problem

`Ready` means the Gateway can route to the server (its spec is valid, its
transport is `http`, and its namespace is watched). It does not mean the
server answers. In testing of `1.0.0-beta.2`, `READY True` was shown for a
crash-looping pod, a wrong endpoint path, and an endpoint the egress policy
refuses (#82). In each case tool discovery failed, but:

- `kubectl get mcpserver` shows only `Ready`;
- `kavryctl list` shows `TOOLS -`, the same as before discovery ran;
- the `ToolsDiscovered` message is terse, for example
  `initialize returned HTTP 404`, with no hint that the path is wrong.

Developers read `READY True` as "my agents can use this".

## Goals

- The failure is visible where people look first: `kubectl get` and
  `kavryctl list`.
- Each common cause gets a message that says what to check.
- Nothing that relies on `Ready` or on the `DiscoveryFailed` reason changes.

## Non-goals

- Changing what `Ready` means, or making `Ready` depend on discovery. The
  E2E contract and SPEC-0004 require that a failed discovery leaves the
  server `Ready`, and servers that skip discovery
  (`CallerTokenRequired`, SPEC-0015) are still routed.
- Probing servers other than through discovery (health checks, synthetic
  calls).
- Pod-level diagnosis (reading pod status or events): the Operator has no
  access to pods and gains none.

## Requirements

### Visibility

- **SPEC-0027-R01** `kubectl get mcpserver` MUST show a `Discovery` column
  with the `ToolsDiscovered` condition's reason (for example `Discovered`,
  `DiscoveryFailed`, `CallerTokenRequired`), in both copies of the CRD.
- **SPEC-0027-R02** `kavryctl list` MUST show in `TOOLS` the number of tools
  when `ToolsDiscovered` is `True`, the condition's reason when it is
  `False`, and `-` only when the condition is absent.
- **SPEC-0027-R03** The `ToolsDiscovered` reasons and the `Ready` condition
  MUST stay as they are: `DiscoveryFailed` for every cause below, and
  `Ready` unaffected by discovery.

### Messages

Each failed discovery MUST set a `ToolsDiscovered` message that names the
cause and the next step:

- **SPEC-0027-R04** An HTTP `404` to `initialize`: the path that answered
  `404`, and that many MCP frameworks serve at `/mcp`.
- **SPEC-0027-R05** A refused connection or one that cannot be made: the
  host and port, and to check that the Service has ready pods.
- **SPEC-0027-R06** A host that does not resolve: the host, and to check
  the Service name and namespace.
- **SPEC-0027-R07** An address the egress policy refuses: that the endpoint
  resolves to an address kind the egress policy refuses for MCP endpoints
  (link-local, cloud metadata, loopback), naming the kind.
- **SPEC-0027-R08** An HTTP `401` or `403`: that the server needs a
  credential, and `spec.credential` or `spec.tokenExchange`.
- **SPEC-0027-R09** A timeout: the time waited.
- **SPEC-0027-R10** An answer that is not MCP (not JSON-RPC, or not
  `application/json` or `text/event-stream`): that the endpoint does not
  look like an MCP Streamable HTTP endpoint.

### Bounds

- **SPEC-0027-R11** Messages MUST NOT include response bodies, header
  values, credentials, query strings, or resolved IP addresses, and MUST be
  at most 512 characters.

## Acceptance criteria

- Given a server whose Deployment has no ready pods, then `kubectl get
  mcpserver` shows `Ready True` and `Discovery DiscoveryFailed`,
  `kavryctl list` shows `TOOLS DiscoveryFailed`, and the message names the
  host and port and says to check for ready pods (R01, R02, R05).
- Given an endpoint `http://svc.ns.svc:8080/` for a server that serves at
  `/mcp`, then the message names `/` and suggests `/mcp` (R04).
- Given an endpoint whose name resolves to `169.254.169.254`, then the
  message says the egress policy refuses cloud metadata addresses and does
  not contain the address (R07, R11).
- Given a server that needs a token, then the message names
  `spec.credential` and `spec.tokenExchange` (R08).
- Given any failure, then `Ready` and the reason are unchanged from today
  (R03).

## Security and privacy

Messages come from the Operator, not from the MCP server, and never quote
the server's answers (R11), so a server cannot write into another
operator's view. Host names and paths come from `spec.endpoint`, which
readers of the status can already see. Resolved addresses stay out, so the
status does not map the cluster network. No new access.

## Compatibility

A CRD change (a printer column only) in both copies of the schema. No new
fields, reasons, or conditions, so alerts on `DiscoveryFailed`
(`KavryntMCPToolDiscoveryFailing`, the console's `DiscoveryFailing`) keep
working. Tools that parse `kavryctl list` output see reasons in `TOOLS`
where they saw `-`.

## Documentation

Operator component docs (conditions and messages), `kavryctl` docs (`list`),
the `KavryntMCPToolDiscoveryFailing` and `KavryntMCPServerNotReady`
runbooks, and the testing runbook's troubleshooting.

## Open questions

- Should `Ready` be renamed or complemented (for example a `Serving`
  condition that is `True` only when discovery succeeds)? Recommended: not
  now. The column and messages close the gap without breaking anything
  that reads `Ready`. Revisit with users. The owner decides.
- Should discovery run again sooner after a failure, so a fixed path shows
  up faster? Recommended: out of scope, unless the current retry interval
  proves too slow in practice.
