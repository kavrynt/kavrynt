# SPEC-0013: Audit events

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("Audit") |
| Supersedes | none |

## Summary

The Gateway writes one structured audit event for every MCP request it
decides on: who called (SPEC-0006), which server, method, and tool, what
was decided and why (SPEC-0012), and how it ended. Events go to standard
output as JSON lines, separate from operational logs on standard error,
so any cluster log pipeline can collect them. Each Gateway instance chains
its events with SHA-256 hashes, and `kavryctl audit verify` detects a
missing, reordered, or altered event.

## Problem

There is no record of who called which tool. Security teams cannot answer
"which agent deleted this ticket" or prove that a denied call was denied.
Traffic metrics and `/v1/traffic` are aggregate or short-lived and carry no
identity.

## Goals

- Every MCP request decision is recorded with identity, tool, decision,
  and outcome.
- Tampering with or losing recorded events is detectable.
- No tool arguments, results, or credentials are ever recorded.

## Non-goals

- Sending events to Kavrynt Cloud (needs a Cloud ingestion API; own spec
  with `kavrynt-cloud`).
- Recording arguments or results, even per tool (ADR-0003 allows it as an
  explicit setting later; not in this spec).
- Long-term storage, search, or retention: the customer's log pipeline
  owns that.
- Auditing console logins or `/v1/*` reads.

## Requirements

### Emission

- **SPEC-0013-R01** With `gateway.audit.enabled=true` (flag `--audit`), the
  Gateway MUST write exactly one audit event per request to `/mcp/`,
  including requests refused by authentication, policy, classification, or
  a missing upstream credential, when the request completes.
- **SPEC-0013-R02** Events MUST be single-line JSON objects on standard
  output with `"type": "kavrynt.audit.v1"`; operational logs MUST stay on
  standard error.

### Content

- **SPEC-0013-R03** Each event MUST contain: `type`, `time` (RFC 3339 with
  milliseconds, UTC), `instance`, `seq`, `request_id`, `subject`,
  `client_id`, `issuer`, `route` (`namespace`, `name`), `http_method`,
  `mcp_method`, `tool`, `decision` (`allow`, `deny`, `unauthenticated`,
  `forbidden`, `invalid`, `unavailable`), `reason`, `policy_revision`,
  `outcome` (the traffic outcome), `status`, `duration_ms`, `bytes_in`,
  `bytes_out`, `prev_hash`, and `hash`. Fields that do not apply MUST be
  present and empty.
- **SPEC-0013-R04** Events MUST NOT contain tool arguments, results,
  request or response bodies, tokens, credential values, cookies, or
  request headers.
- **SPEC-0013-R05** The Gateway MUST return the `request_id` to the client
  in an `X-Request-Id` response header, so a call can be matched to its
  event.

### Integrity

- **SPEC-0013-R06** For each instance, `seq` MUST start at 1 and increase by
  1; `prev_hash` MUST be the previous event's `hash` (64 zeros for the
  first); `hash` MUST be the hex SHA-256 of `prev_hash` followed by the
  event's canonical JSON without the `hash` field. The canonical form MUST
  be documented.
- **SPEC-0013-R07** `kavryctl audit verify` MUST read events from a file or
  standard input, group them by instance, and report each break (gap,
  reordering, or hash mismatch) with the instance and `seq`; it MUST exit
  non-zero when it finds one.

### Back-pressure

- **SPEC-0013-R08** Writing events MUST NOT block request handling beyond a
  bounded in-memory queue (default 10,000 events). When the queue is full,
  with `gateway.audit.onOverflow=reject` (default) new `/mcp/` requests MUST
  get `503` and not be forwarded until there is room; with `drop` they MUST
  be served and the dropped events counted.
- **SPEC-0013-R09** The Gateway MUST export
  `kavrynt_gateway_audit_events_total`,
  `kavrynt_gateway_audit_overflow_total{action}`, and the queue length, and
  MUST flush queued events on graceful shutdown.

### End to end

- **SPEC-0013-R10** With authentication, policy, and audit on, an allowed
  and a denied call MUST each produce one event with the right subject,
  tool, and decision and no argument values, and the collected Gateway
  output MUST pass `kavryctl audit verify`.

## Acceptance criteria

- Given client `support-agent` calls `tools/call search` with argument
  `{"q": "secret-value"}` and is allowed, then one event has
  `client_id: support-agent`, `tool: search`, `decision: allow`,
  `outcome: ok`, and the string `secret-value` appears nowhere in the
  output (R01, R03, R04).
- Given client `etl` is denied, then its event has `decision: deny` and the
  policy reason (R01, R03).
- Given an agent without a token, then an event with `decision:
  unauthenticated` and empty identity (R01, R03).
- Given a captured log with one event removed or edited, then
  `kavryctl audit verify` names the instance and `seq` and exits 1 (R06,
  R07).
- Given a blocked standard output with `onOverflow=reject`, when the queue
  fills, then new calls get `503` (R08).

## Security and privacy

Audit events identify people and agents (`subject`, `client_id`); they are
personal data in many jurisdictions, and the docs say so. They never carry
arguments, results, or secrets (R04), and the e2e greps for an argument
value to prove it. The hash chain detects alteration or loss by anyone who
did not also recompute every later hash; it is tamper-evident, not
tamper-proof, until events leave the cluster (Cloud delivery spec). Rejecting
requests on overflow makes audit fail closed: a call is never served
without its record, unless an operator chooses `drop`.

## Compatibility

Opt-in. New values `gateway.audit.enabled`, `gateway.audit.queueSize`,
`gateway.audit.onOverflow`; new flags. New response header `X-Request-Id`
on `/mcp/` responses (also when audit is off). New `kavryctl audit verify`
command. The event schema is versioned by `type`; changes add a new type.
No CRD change.

## Documentation

Gateway component doc (audit section, schema, canonical form), `kavryctl`
doc, chart README, log pipeline examples (Fluent Bit filter on `type`),
README security posture, CLAUDE.md "What works today".

## Decisions

- Standard output as the only sink in this spec: works with every cluster
  log pipeline, no extra storage or permissions.
- The event format lives in a shared API package (`api/audit/v1`) so
  `kavryctl` can verify it without importing Gateway internals.
- Default overflow behavior is `reject` (fail closed).
