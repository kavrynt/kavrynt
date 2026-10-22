# SPEC-0002: Console v2: operations UI

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-22 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) (later phases only) |
| Supersedes | none |

## Summary

Turn the Gateway console from an inspection page into the place an operator
goes first when an MCP server or a tool call misbehaves. It adds an overview
that surfaces what needs attention, a cross-server tool catalog, per-tool
traffic statistics, shareable views, and diagnostics, using only data the
Gateway already serves. It stays read-only, embedded, and build-free, and
keeps every [SPEC-0001](../0001-gateway-console/spec.md) requirement.

## Problem

The v1 console (SPEC-0001) answers "what is registered" but not "what is
wrong":

- A server that is not routed, or whose tool discovery failed, is visible only
  if the operator opens it and reads its conditions.
- Tools are listed per server. Agent builders asking "which server has a tool
  for X" must open each server.
- Traffic shows individual requests, not which tool is failing or slow, and
  outcome names such as `rpc_error` or `upstream_unreachable` are not
  explained.
- Filters live in memory, so a view cannot be linked in an issue or a chat.
- The page does not say that traffic covers one replica only, and does not
  warn when it is reachable without authentication from a non-local address.

## Goals

- Show what needs attention on the first screen.
- Find any tool across all servers in one search.
- Explain failures: per-tool error rate and latency, outcome definitions, and
  a copyable diagnostics summary.
- Make every view linkable.
- State the console's limits (one replica, no login) where they matter.

## Non-goals

- Login, user identity, and per-user authorization. They need ADR-0003 client
  authentication first and get their own spec (see "Later phases" in the
  plan).
- Writing to Kubernetes: registering, editing, or deleting `MCPServer`
  resources stays with `kubectl`, `kavryctl`, and GitOps. The Gateway stays
  read-only on the Kubernetes API.
- Durable or cross-replica history, alerting, and dashboards. Prometheus and
  the bundled Grafana dashboard cover these.
- Multi-cluster inventory, which belongs to Kavrynt Cloud.
- New Gateway endpoints. Phase 1 uses `/v1/servers`, `/v1/traffic`,
  `/readyz`, and `/version` as they are.

## Requirements

### Overview

- **SPEC-0002-R01** The console MUST open on an Overview view (`#/overview`)
  that shows Gateway readiness and version; the number of servers, routed
  servers, and tools; and, for the recent-traffic window, the request count,
  error rate, and p95 latency.
- **SPEC-0002-R02** The Overview MUST list every server that is not routed,
  or whose `ToolsDiscovered` condition is `False`, with the condition reason
  and message and a link to the server.
- **SPEC-0002-R03** The Overview MUST list the five tools with the most
  non-`ok` outcomes in the recent-traffic window, with their route and error
  rate, and MUST show nothing for tools without errors.

### Tool catalog

- **SPEC-0002-R04** A Tools view (`#/tools`) MUST list every discovered tool
  of every server with tool name, server route, routing state, and
  description, searchable by tool name, description, and route.
- **SPEC-0002-R05** Each tool in the catalog MUST link to the Playground with
  that route and tool preselected, and the link MUST be disabled with an
  explanation when the server is not routed.

### Traffic

- **SPEC-0002-R06** The Traffic view MUST filter by route, method, tool, and
  individual outcome value.
- **SPEC-0002-R07** The Traffic view and each server's Traffic tab MUST show a
  per-tool summary: calls, error rate, p50 and p95 latency.
- **SPEC-0002-R08** Every outcome value the console shows MUST have a
  plain-language definition available from the Traffic view, matching the
  Gateway component documentation.
- **SPEC-0002-R09** Every view that shows traffic MUST state that it covers
  only the Gateway replica that served the console and at most its last 500
  requests.

### Navigation and sharing

- **SPEC-0002-R10** Filters and search terms in the Servers, Tools, and
  Traffic views MUST be encoded in the URL hash, so reloading or sharing the
  URL restores the view.
- **SPEC-0002-R11** A server's detail view MUST offer "Copy diagnostics":
  plain text with the console version, the server's route, transport,
  conditions, tool names, and its recent traffic metadata. It MUST NOT include
  the endpoint's query string, tool input values, or responses.

### Safety

- **SPEC-0002-R12** When the console is loaded from a host other than
  `localhost`, `127.0.0.1`, or `::1`, it MUST show a persistent notice that it
  has no authentication and is reachable by anyone who can reach the Gateway.
- **SPEC-0002-R13** The console MUST NOT write tool arguments, tool results,
  or JSON-RPC messages to `localStorage`, `sessionStorage`, IndexedDB, or
  cookies. Playground history MUST live in memory only.
- **SPEC-0002-R14** The console MUST stop polling while the page is hidden
  and refresh once when it becomes visible again.

### Quality

- **SPEC-0002-R15** Every action MUST be reachable and operable by keyboard
  with a visible focus indicator, and every control MUST have an accessible
  name.
- **SPEC-0002-R16** Text and interactive elements MUST meet WCAG 2.2 AA
  contrast in the light and the dark theme.
- **SPEC-0002-R17** Every view MUST be usable at 360 CSS pixels wide without
  horizontal page scrolling; wide tables scroll inside their own container.
- **SPEC-0002-R18** The console MUST remain plain HTML, CSS, and JavaScript
  with no build step and no external assets, and its static files MUST total
  at most 256 KiB uncompressed.

## Acceptance criteria

- Given a server whose endpoint is rejected by the Operator, when an operator
  opens `/ui/`, then the Overview lists that server with the condition reason
  and links to it (R01, R02).
- Given `tools/call` errors on tool `search` of `team-a.docs`, when the
  operator opens the Overview, then `search` on `team-a.docs` is listed with
  its error rate (R03).
- Given ten servers, when the operator searches the Tools view for `search`,
  then every server exposing a matching tool is listed, and choosing one opens
  the Playground with that tool selected (R04, R05).
- Given the Traffic view filtered to outcome `upstream_unreachable`, when the
  URL is opened in a new tab, then the same filter is applied and the outcome
  is explained (R06, R08, R10).
- Given the console served through an Ingress host, when it loads, then the
  "no authentication" notice is shown (R12).
- Given a Playground call with argument `secret-argument`, when browser
  storage is inspected, then the value is not present (R13).

## Security and privacy

All rendered data is untrusted and stays under SPEC-0001-R04 and
SPEC-0001-R05: same-origin CSP and text-only insertion. Phase 1 adds no
endpoint, no credential, and no Kubernetes access. The diagnostics text
(R11) is metadata only and is copied to the clipboard by an explicit user
action; it is never sent anywhere by the console. R12 makes the existing
unauthenticated exposure visible instead of silent. R13 keeps tool inputs,
which may contain secrets, out of persistent browser storage on shared
machines.

## Compatibility

No CRD, Helm value, or API change. The default landing view moves from
Servers to Overview; existing `#/servers`, `#/playground`, and `#/traffic`
links keep working.

## Documentation

When `Implemented`: update the Console section of
[the Gateway component doc](../../docs/components/gateway/README.md#console)
(views, outcome definitions), the README "Open the console" step, and
`docs/DEMO.md` if the walkthrough shows the console.

## Open questions

- How are browser-side requirements tested? The plan proposes Node's built-in
  test runner for pure modules and one headless-browser smoke test in the
  Kind job. Node is not a project toolchain requirement today. Owner decides.
- Should the Overview become the default view, or stay one click away from
  Servers? The spec assumes default.
