# SPEC-0016 plan: Policy-filtered tools/list

## Design

- **Switch (R01).** `--policy-filter-tools-list` (default `false`), chart
  value `gateway.policy.filterToolsList` (schema: boolean; rendering fails
  when it is true and `policy.enabled` is false).
  `api.Handler.EnablePolicy(engine, filter bool)` keeps both.
- **When.** In the proxy path, a `POST` whose body is a `tools/list`
  request, with the policy on and the switch set, is filtered. The
  upstream request drops `Accept-Encoding`, so Go's transport asks for and
  decodes gzip itself and the body arrives uncompressed.
- **Filter (R01, R03).** `internal/gateway/api/toolsfilter.go`:
  `filterTools(ctx, decide, input, message []byte) ([]byte, removed int,
  err)`. It decodes the JSON-RPC response into `map[string]json.RawMessage`
  and `result` the same way, so every field other than `tools` (including
  `nextCursor` and `_meta`) is copied byte for byte. For each tool it reads
  only `name` and asks the policy with `mcp_method: tools/call`,
  `tool: <name>`, and the request's identity and route. Allowed tools keep
  their original bytes and order. A JSON-RPC error response passes
  unchanged.
- **JSON answers (R02, R04).** The response body is read up to
  `maxInspectBytes`; within the limit it is filtered and written with the
  new length (`Content-Length` from the upstream is dropped).
- **Event streams (R02, R04).** `sseFilter` reads the stream event by
  event (lines up to a blank line, at most `maxInspectBytes` per event) and
  writes and flushes each one. Only the event whose `data` is a JSON-RPC
  response with the request's `id` is filtered; notifications and server
  requests pass unchanged.
- **Fail closed (R04).** One context with a 500 ms deadline covers all
  evaluations of one answer. A timeout, a body or event over the limit, or
  a result that is not `{"tools": [...]}` replaces the answer with
  `{"jsonrpc":"2.0","id":<id>,"result":{"tools":[]}}` and logs the reason
  at warn level (no tool names, no body).
- **Records (R05).** Counter
  `kavrynt_gateway_policy_filtered_tools_total{route}` (registered with the
  policy collectors); `auditRecord.filteredTools` becomes
  `auditv1.Event.FilteredTools` (`filtered_tools`, omitted when zero).

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/api/toolsfilter.go`, `toolsfilter_test.go` | Filter, SSE reader, tests |
| `internal/gateway/api/api.go`, `audit.go` | Filter step, audit field |
| `api/audit/v1/event.go`, tests | `filtered_tools` |
| `cmd/gateway/main.go` | Flag |
| Gateway chart values, schema, Deployment, `charts/kavrynt/tests/policy_test.yaml` | Value |
| `scripts/e2e-kind.sh` | Filtered list in the policy section |
| `docs/components/gateway/README.md`, chart README | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0016-R01 | `toolsfilter_test.go`: allow-list policy removes `delete_all`; switch off leaves the list whole; `e2e-kind.sh` lists tools as an allowed and a denied client |
| SPEC-0016-R02 | `toolsfilter_test.go`: SSE answer with a notification before the response |
| SPEC-0016-R03 | `toolsfilter_test.go`: `nextCursor`, `_meta`, and unknown fields kept |
| SPEC-0016-R04 | `toolsfilter_test.go`: slow policy, oversized body, malformed result |
| SPEC-0016-R05 | `toolsfilter_test.go`: counter and audit field |
| (chart) | `charts/kavrynt/tests/policy_test.yaml`: flag passed; refused without policy |

## Rollout

Off by default. Release notes: the switch, fail-closed behaviour, the new
audit field and the matching `kavryctl`.

## Risks

- A policy that denies `tools/call` for everything hides every tool, which
  is the intended outcome but may surprise. The docs say so.
