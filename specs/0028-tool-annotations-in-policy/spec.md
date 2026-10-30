# SPEC-0028: Tool annotations in policy

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-27 |
| Issue | #83 |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

The Operator records the behavior hints MCP servers report for their tools
(`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`), and
the tool authorization policy receives the called tool's hints. A policy can
then allow read-only tools for trusted servers without listing every tool
by name. This is the "tool risk metadata" input ADR-0003 names.

## Problem

MCP tools can carry `annotations` that describe their behavior. The Operator
drops them when it records `status.tools`, and the policy input has no field
for them (SPEC-0012). A rule such as "this agent may only read" must list
every tool name by hand, per server, and silently goes stale when a server
adds a tool (#83). The policy example `read-only.rego` therefore denies every
tool call.

## Goals

- Policies can decide on a tool's reported behavior.
- Policies can tell a tool the Operator discovered from one it has not seen.
- Nobody mistakes hints for a security boundary.

## Non-goals

- Verifying hints: they are the server's own claims and stay untrusted.
- Kavrynt-side risk labels (an operator marking a tool as dangerous). That
  would be a separate spec, possibly authored in Kavrynt Cloud.
- Sending annotations to Kavrynt Cloud in the Operator connector's
  inventory. That is a later change to the connector's contract.
- Approvals (`require_approval`, SPEC-0025).

## Requirements

### Recording

- **SPEC-0028-R01** The Operator MUST record in `status.tools[].annotations`
  each of `readOnlyHint`, `destructiveHint`, `idempotentHint`, and
  `openWorldHint` that the server reports as a JSON boolean, and nothing
  else from `annotations`. A hint that is absent or not a boolean MUST be
  absent from the status.
- **SPEC-0028-R02** Recorded annotations MUST count toward the existing
  bounds of the status (`MaxStatusBytes`, 200 tools).

### Policy input

- **SPEC-0028-R03** For `tools/call`, the policy input MUST include
  `tool_annotations`: the hints recorded for the called tool on that route,
  with absent hints absent (not `false`), and `{}` when none are recorded.
- **SPEC-0028-R04** The policy input MUST include `tool_discovered`: `true`
  when the called tool is in the route's `status.tools`, `false` otherwise
  (discovery has not run, failed, was skipped, or the tool is new or beyond
  the bound).
- **SPEC-0028-R05** `tool_annotations` and `tool_discovered` MUST come only
  from the `MCPServer` status, never from the request.
- **SPEC-0028-R06** Filtered `tools/list` evaluations (SPEC-0016) MUST use
  the same fields from the same source for each tool, so a listed tool is
  one the caller may call.
- **SPEC-0028-R07** For other methods, `tool_annotations` MUST be `{}` and
  `tool_discovered` `false`.

### Showing and using them

- **SPEC-0028-R08** The console's tool views MUST show each recorded hint.
- **SPEC-0028-R09** `examples/policy` MUST include a policy, tested in CI,
  that allows `tools/call` only for discovered tools with
  `readOnlyHint: true` on an allow-list of servers, and the read methods of
  `read-only.rego` everywhere.

## Acceptance criteria

- Given a server whose `search` tool reports `readOnlyHint: true` and whose
  `delete_all` reports `destructiveHint: true`, then `status.tools` records
  them, and the console shows them (R01, R08).
- Given a tool whose `annotations` has `readOnlyHint: "yes"` and an
  unknown `dangerous: true`, then neither is recorded (R01).
- Given the example policy with the server allow-listed, then `search` is
  allowed and `delete_all` is denied, and a tool the server added after the
  last discovery is denied (`tool_discovered: false`) (R03, R04, R09).
- Given a caller that sends `annotations` in its `tools/call` params, then
  the policy input is unchanged by them (R05).
- Given `filterToolsList` and the example policy, then `tools/list` shows
  `search` and not `delete_all` (R06).

## Security and privacy

Hints are claims by the MCP server about itself. A malicious or buggy
server can mark a destructive tool as read-only, so policies MUST NOT trust
hints from servers they do not trust. The example allow-lists servers for
that reason (R09), and the docs say so. The policy sees the hints of the
last discovery: a server that changes a tool's behavior between discoveries
is judged on its earlier claim. `tool_discovered` lets a policy deny tools
it has not seen. Request content cannot affect either field (R05).
Annotations are booleans, so they add no free text to status or logs.

## Compatibility

A CRD change in both copies of the schema: the optional
`status.tools[].annotations` object. Policies that ignore the new input
fields are unaffected. Servers that report no annotations get `{}`.
Upgrading needs no action. The status gains the hints at the next
discovery.

## Documentation

Gateway component docs (policy input table, with the warning that hints are
self-reported and the MCP defaults for absent hints: `readOnlyHint` false,
`destructiveHint` true, `idempotentHint` false, `openWorldHint` true),
Operator docs (`status.tools`), `examples/policy`, ADR-0003 (risk metadata
now available).

## Open questions

- Should the input apply MCP's defaults for absent hints instead of leaving
  them absent? Recommended: leave them absent (R03) and document the
  defaults, so a policy can tell "the server said nothing" from "the server
  said false". The owner decides.
- Should `title` be recorded too? Recommended: no. It is free text that
  policies should not decide on.
