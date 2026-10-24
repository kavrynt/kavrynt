# SPEC-0016: Policy-filtered tools/list

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

With the tool authorization policy on (SPEC-0012), a caller's `tools/list`
shows only the tools the policy would let that caller call, so agents do not
plan with tools they will be denied.

## Problem

SPEC-0012 denies `tools/call` of disallowed tools, but `tools/list` still
returns every tool. Agents pick tools they cannot use, fail, and retry;
tool names and descriptions also reveal capabilities a caller should not
know about.

## Goals

- `tools/list` answers contain only tools the caller may call.
- Filtering never lets a tool through when the policy cannot decide.

## Non-goals

- Filtering resources or prompts.
- Changing `tools/call` enforcement (SPEC-0012 stays the authority).

## Requirements

- **SPEC-0016-R01** With `gateway.policy.filterToolsList=true` (off by
  default; requires a policy), the Gateway MUST evaluate the policy for each
  tool in a `tools/list` result as if the caller called it
  (`mcp_method: tools/call`, `tool: <name>`), and remove denied tools.
- **SPEC-0016-R02** Filtering MUST apply to JSON responses and to the JSON-RPC
  response in a `text/event-stream` answer; other events MUST pass
  unchanged.
- **SPEC-0016-R03** `nextCursor` and every field other than removed tools
  MUST be preserved.
- **SPEC-0016-R04** All evaluations for one response MUST finish within 500
  ms; if they do not, or the response is larger than the inspection limit or
  not a valid `tools/list` result, the Gateway MUST answer with an empty
  `tools` list and log the reason (fail closed).
- **SPEC-0016-R05** The number of removed tools MUST be counted in
  `kavrynt_gateway_policy_filtered_tools_total{route}` and recorded in the
  request's audit event.

## Acceptance criteria

- Given the client allow-list policy and a server with `search` and
  `delete_all`, when `support-agent` lists tools, then only `search` is
  returned (R01).
- Given a policy that times out, then `tools/list` returns no tools (R04).

## Security and privacy

Fails closed. Response bodies are inspected in memory only, within the
existing 1 MiB limit; nothing is stored.

## Compatibility

New value `gateway.policy.filterToolsList`, new metric and audit field
(`filtered_tools`, a new event type version if the schema changes).

## Documentation

Gateway component doc (policy), chart README.

## Decisions

- No cache: each `tools/list` answer is filtered with fresh evaluations, so
  a policy update applies to the next list at once. Evaluating a prepared
  Rego query per tool takes microseconds; R04 bounds the worst case.
- The audit event gains an optional `filtered_tools` count, set only on
  filtered answers; the event type stays `kavrynt.audit.v1`. `kavryctl
  audit verify` from earlier releases reports such events as breaks, so
  verify with the `kavryctl` of the same release.
- The Gateway asks the MCP server for an uncompressed answer when it
  filters, so a compressed body never forces the fail-closed path.
