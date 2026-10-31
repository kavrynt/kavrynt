# SPEC-0036 plan: Policy denials that MCP clients can read

## Decisions

- A JSON-RPC error with `200`, not a tool result with `isError: true`: a
  denial refuses the request itself, applies to every method (not only
  `tools/call`), and SDKs surface JSON-RPC errors to agents as exceptions
  with the message (`McpError` in the Python SDK).
- Requests without a JSON-RPC request (notifications, responses, `GET`,
  `DELETE`) keep `403`: there is no response to hold the error.

## Design

`Handler.authorize` answers a denial with `200` when the request is a
`POST` whose body has a `method` and an `id` (a JSON-RPC request), and
`403` otherwise. The body (`writeRPCError`, code `-32001`), the `denied`
outcome, the audit decision, and the metrics are unchanged; the audit
event records the status sent.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/api/api.go` | Status of a denial |
| `internal/gateway/api/*_test.go` | Tests expect `200` for denied requests; new test for `403` cases |
| `scripts/e2e-kind.sh` | The denied call and its audit event expect `200` |
| Docs | Gateway component docs, `docs/TESTING.md`, SPEC-0012-R08 note |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `api.TestPolicyDeniesWithAJSONRPCError`, `scripts/e2e-kind.sh` (denied echo) |
| R02 | `api.TestDenialsWithoutARequestStay403` |
| R03 | `api.TestPolicyDeniesWithAJSONRPCError`, `api.TestEveryDecisionIsAudited` |

Checked with the official MCP Python SDK against real servers on Kind: the
agent receives `McpError: denied by policy: <reason>` instead of
`HTTPStatusError: 403`.

## Rollout

No flag: a behavior change in the next release. Release notes: "A policy
denial of a JSON-RPC request is now answered `200` with the JSON-RPC
error, so MCP clients show the reason; scripts that checked for `403`
must read the error instead (SPEC-0036)."

## Risks

Monitoring that counted `403` answers as denials misses them; the `denied`
outcome metric and audit `decision: deny` remain the record.
