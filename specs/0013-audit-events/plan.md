# SPEC-0013 plan: Audit events

## Design

- **Format package `api/audit/v1`.** `Event` struct with JSON tags in a
  fixed field order, `Canonical(e Event) []byte` (JSON of the struct with
  `Hash` omitted; the struct has no maps, so `encoding/json` writes fields
  in declaration order), and `Hash(prev string, e Event) string`. Shared
  by the Gateway and `kavryctl`; it imports only the standard library.
- **Gateway writer `internal/gateway/audit`.** `Writer` with a buffered
  channel (queue), one goroutine that assigns `seq`, chains hashes, and
  writes to an `io.Writer` (stdout) with a `bufio.Writer` flushed per
  event. `Enqueue` is non-blocking: when full it returns `ErrOverflow`
  (reject) or increments the drop counter (drop) (R08). `Close(ctx)`
  drains (R09). Sequencing happens in the writer goroutine so the chain
  order is the write order (R06).
- **Handler.** A per-request `auditRecord` filled as the request moves
  through authentication (SPEC-0006), classification, policy (SPEC-0012),
  credentials (SPEC-0011), and proxying; emitted in a deferred call.
  Before authentication, `Reserve()` checks for queue room so an overflow
  rejects with `503` before forwarding (R08). `request_id` is 16 random
  bytes, hex, set as `X-Request-Id` (R05).
- **kavryctl (R07).** `kavryctl audit verify [file]`: reads lines, skips
  lines that are not JSON or not `kavrynt.audit.v1` (log collectors may
  interleave), groups by `instance`, checks `seq`, `prev_hash`, `hash`.
- **Chart.** Values, schema, flags. No volume: standard output.
- **End to end (R10).** `e2e-kind.sh` collects `kubectl logs` of the
  Gateway pods (which interleave stdout and stderr; the verifier skips
  non-audit lines), greps for the argument value (expect none), and runs
  the locally built `kavryctl audit verify`.

## Affected files

| Path | Change |
| --- | --- |
| `api/audit/v1/` | New format package and tests |
| `internal/gateway/audit/` | New writer and tests |
| `internal/gateway/api/api.go` | Audit record, `X-Request-Id`, overflow check |
| `cmd/gateway/main.go` | Flags, shutdown flush |
| `internal/kavryctl/cli/` | `audit verify` command and tests |
| Gateway chart values, schema, Deployment, tests | Audit values |
| `scripts/e2e-kind.sh` | Audit section |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0013-R01, R02, R03, R04, R05 | `api_test.go` per refusal path and success, field presence, body scan |
| SPEC-0013-R06 | `api/audit/v1` hash and canonical form tests |
| SPEC-0013-R07 | `kavryctl` verify tests (gap, reorder, edit, interleaved logs) |
| SPEC-0013-R08, R09 | `audit` writer tests with a blocking writer, drain on close |
| SPEC-0013-R10 | `e2e-kind.sh` audit section |

## Rollout

Off by default; on in `examples/helm/values-production.yaml`. Release
notes: event schema, log pipeline filter, overflow behavior.

## Risks

- A slow log pipeline can block stdout and, with `reject`, cause `503`s.
  Detected by `kavrynt_gateway_audit_overflow_total` (alert added to
  SPEC-0010's rule set when this ships).
