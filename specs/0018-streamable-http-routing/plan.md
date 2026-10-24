# SPEC-0018 plan: MCP Streamable HTTP routing

## Design

- **Versions.** `internal/mcpmeta`: `ProtocolVersions` (oldest first),
  `HeaderProtocolVersion = "2026-07-28"` (the first that requires
  `Mcp-Method`), `DefaultProtocolVersion = "2025-03-26"` (assumed without
  the header), and `KnownProtocolVersion(v)`. The Operator's discovery
  version comes from the same list.
- **Check (R01 to R04).** `internal/gateway/api/protocol.go`:
  `checkProtocol(r, call) error` runs in the proxy path after the request is
  inspected and before the loop, transport, and policy steps:
  1. `MCP-Protocol-Version` set and unknown: `400` naming the supported
     versions (every method).
  2. POST only: a body that is a JSON array is a batch: `400`.
  3. POST with a readable single message that has a `method`: `Mcp-Method`
     present and different from the body, or `Mcp-Name` present on
     `tools/call` and different from `params.name`: `400`. `Mcp-Method`
     absent while the version is `2026-07-28` or later: `400`.
  Bodies the Gateway cannot read (over 1 MiB, not JSON) are forwarded
  unchecked unless a policy is on (SPEC-0012-R09 stays as it is).
  `inspectBody` gains `batch bool` so arrays are told apart from other
  unreadable bodies.
- **Refusals (R06).** `400` with the stable `{"error": ...}` body; outcome
  `protocol_error` (new constant) in metrics and recent traffic; the audit
  record gets decision `invalid` and the error as reason.
- **Pass-through (R05).** No change in behavior: `GET` and `DELETE` have no
  body check, and `Last-Event-ID` and `Mcp-Session-Id` are end-to-end
  headers that `copyHeaders` keeps. Tests pin it.
- **Migration switch.** `--strict-protocol` (default `true`), chart value
  `gateway.config.strictProtocol` (schema: boolean). When `false`, a
  failed check is logged at debug level and the request is forwarded; with
  a policy on, `authorize` still applies the SPEC-0012 checks.

## Affected files

| Path | Change |
| --- | --- |
| `internal/mcpmeta/mcpmeta.go` | Protocol versions |
| `internal/operator/discovery/discovery.go` | Use the shared version constant |
| `internal/gateway/api/protocol.go`, `protocol_test.go` | Checks and tests |
| `internal/gateway/api/api.go`, `traffic.go` | Call the check; `batch`; outcome |
| `cmd/gateway/main.go` | `--strict-protocol` |
| Gateway chart values, schema, Deployment, `charts/kavrynt/tests/gateway_test.yaml` | Value and flag |
| `internal/gateway/console/static/stats.js` | Label for the new outcome |
| `docs/components/gateway/README.md` | Routing rules |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0018-R01 | `protocol_test.go`: mismatched `Mcp-Method` and `Mcp-Name`, no policy |
| SPEC-0018-R02 | `protocol_test.go`: no `Mcp-Method` with `2025-06-18` (forwarded) and `2026-07-28` (refused); a client JSON-RPC response without it (forwarded) |
| SPEC-0018-R03 | `protocol_test.go`: batch body |
| SPEC-0018-R04 | `protocol_test.go`: unknown version, the body lists the supported ones |
| SPEC-0018-R05 | `protocol_test.go`: `GET` stream and `DELETE` forwarded with `Last-Event-ID` and `Mcp-Session-Id` |
| SPEC-0018-R06 | `protocol_test.go`: metric outcome and audit decision; `--strict-protocol=false` forwards |
| (chart) | `charts/kavrynt/tests/gateway_test.yaml` passes `--strict-protocol` |

## Rollout

On by default. Release notes: clients that send batches or mismatched
headers are refused; `gateway.config.strictProtocol=false` during
migration.

## Risks

- An MCP revision after `2026-07-28` is refused until it is added to the
  list. Mitigation: one constant to change; the error names the supported
  versions.
