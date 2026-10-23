# SPEC-0004: Reported MCP server version

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-23 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

Record the name and version an MCP server reports in its `initialize`
response (`serverInfo`) in `MCPServer` status, and show it next to the
declared `spec.version` in the Gateway console and `kavryctl inspect`, so
platform engineers can see which build is actually running and spot when it
differs from what the manifest claims.

## Problem

`spec.version` is whatever the author typed into the manifest. Nothing checks
it against the running server. During the hands-on review of the console
(SPEC-0002), the only place the real version appeared was the Playground's
session badge (`kavrynt-demo-incidents 0.1.0`), after opening a session by
hand for each server. An operator who rolls out a new server build and
forgets to bump `spec.version`, or the reverse, has no inventory view that
shows the mismatch.

The Operator already performs MCP `initialize` for tool discovery and
receives `serverInfo`; it discards it.

## Goals

- See the version each server reports, for every server, without opening a
  session.
- See when the reported version differs from `spec.version`.

## Non-goals

- Enforcing or alerting on a mismatch. A mismatch is shown, not acted on.
- Version history or rollout tracking. Kavrynt Cloud may build this from
  inventory snapshots later.
- Verifying that `serverInfo` is truthful. It is self-reported by the MCP
  server and untrusted.

## Requirements

- **SPEC-0004-R01** After a successful MCP `initialize`, the Operator MUST
  record `serverInfo.name` and `serverInfo.version` in
  `status.serverInfo.name` and `status.serverInfo.version`, each only when it
  is printable text of at most 128 characters; other values MUST be omitted.
- **SPEC-0004-R02** When `initialize` fails, the Operator MUST keep the last
  recorded `status.serverInfo`, and recording or keeping it MUST NOT change
  the `Ready` condition.
- **SPEC-0004-R03** `GET /v1/servers` MUST include `serverInfo` with `name`
  and `version` for each server whose status has one.
- **SPEC-0004-R04** The console MUST show the reported version next to the
  declared version on server cards and the server detail view, and MUST mark
  the server when both are present and differ.
- **SPEC-0004-R05** `kavryctl inspect` MUST print the reported name and
  version when the status has them.

## Acceptance criteria

- Given a Ready server whose `initialize` reports version `0.2.0` while
  `spec.version` is `0.1.0`, when the Operator has discovered it, then
  `status.serverInfo.version` is `0.2.0` (R01), `/v1/servers` shows it (R03),
  and its console card shows both versions marked as different (R04).
- Given a server reporting a 10,000-character version string, when it is
  discovered, then `status.serverInfo.version` is absent (R01).
- Given a recorded `serverInfo` and a server that then stops answering, when
  discovery fails, then `status.serverInfo` is unchanged and `Ready` is
  unaffected (R02).
- Given a discovered server, when `kavryctl inspect` runs, then the output
  includes the reported name and version (R05).

## Security and privacy

`serverInfo` comes from the MCP server and is untrusted, like tool metadata.
It is bounded before it reaches the Kubernetes API (R01), rendered as text
only in the console (SPEC-0001-R05), and never used for routing or
authorization decisions. No new access: the Operator already writes
`MCPServer` status and the Gateway already reads it.

## Compatibility

Additive CRD change: a new optional `status.serverInfo` object in both CRD
copies (`config/crd/bases` and the chart) and in `api/v1alpha1`. No Helm
value, RBAC, or flag change. Servers discovered before the upgrade show no
reported version until their next discovery.

## Documentation

When `Implemented`: the `MCPServer` API reference (status fields), the
Gateway component doc (`/v1/servers` and the console), and the `kavryctl`
docs.

## Open questions

- Should the Cloud connector include `serverInfo` in inventory snapshots?
  That changes the `/v1/sync/mcpservers` contract owned by `kavrynt-cloud`.
  Owner decides with the Cloud team.
- Should `status.serverInfo` carry the time it was observed, so a stale value
  is visible when discovery keeps failing? The `ToolsDiscovered` condition
  already shows the failure.
