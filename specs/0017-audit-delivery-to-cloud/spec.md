# SPEC-0017: Audit delivery to Kavrynt Cloud

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("Audit"); kavrynt-cloud ADR-0006 |
| Supersedes | none |

## Summary

The Gateway sends its audit events (SPEC-0013) outbound to Kavrynt Cloud in
batches, authenticated with the cluster token, so events leave the cluster
soon after they are written and the hash chains can be verified where an
in-cluster attacker cannot rewrite them.

## Problem

Audit events on standard output are tamper-evident only while nobody can
rewrite every later event. Customers without a log pipeline also have no
durable copy.

## Goals

- At-least-once delivery of every audit event to Kavrynt Cloud.
- Delivery problems never block MCP traffic beyond SPEC-0013's back-pressure.

## Non-goals

- The Kavrynt Cloud ingestion API and storage (kavrynt-cloud spec; this spec
  depends on it).
- Delivering to other sinks (SIEMs): customers use their log pipeline.

## Requirements

- **SPEC-0017-R01** With `gateway.audit.cloud.enabled` (requires
  `gateway.audit.enabled` and the Cloud connector settings
  `gateway.audit.cloud.url` and `gateway.audit.cloud.tokenSecret`, normally
  the same as `operator.cloud`), the Gateway MUST send events in batches (at
  most 500 events or 1 MiB, at least every 10 seconds) to the Cloud ingestion
  endpoint `POST /v1/sync/audit-events` over HTTPS with the cluster token
  from the mounted Secret.
- **SPEC-0017-R02** Events MUST still be written to standard output.
- **SPEC-0017-R03** Failed batches MUST be retried with exponential backoff and
  jitter, honoring `Retry-After`; events are identified by `instance` and
  `seq` so Cloud can drop duplicates.
- **SPEC-0017-R04** Undelivered events MUST be held in a bounded buffer
  (default 100,000); when it is full, the oldest undelivered events are
  dropped and counted (stdout still has them).
- **SPEC-0017-R05** Delivery MUST export
  `kavrynt_gateway_audit_delivery_total{result}`, the buffer length, the
  dropped events, and the time of the last accepted batch, and the chart's
  alerts MUST include `KavryntAuditDeliveryStale` (events waiting and no
  batch accepted for `auditDeliveryStaleSeconds`, default 600) with a
  runbook.
- **SPEC-0017-R06** The token MUST NOT be logged, and requests MUST follow the
  egress guard.

## Acceptance criteria

- Given Cloud answers `503` for five minutes, then batches are retried and
  delivered afterwards with no gap in `seq` per instance (R03).
- Given Cloud is unreachable for long enough to fill the buffer, then the
  oldest events are dropped and counted, MCP traffic is unaffected, and the
  alert fires (R04, R05).

## Security and privacy

Events contain identities (personal data) and leave the customer cluster;
this is opt-in and the docs say what is sent. Transport is HTTPS with the
cluster token; no arguments or results are ever included.

## Compatibility

New values under `gateway.audit.cloud`, a mounted Secret for the Gateway
(no RBAC), new metrics and alert.

## Documentation

Gateway component doc (audit), chart README, runbook, README.

## Decisions

- Delivery lives in the Gateway. Each replica holds its own chain and
  buffer; a volume shared with the Operator would not span nodes, and the
  Operator would have to re-read what the Gateway already has in memory.
- Chain verification on ingestion, and how Cloud shows failures, are
  defined by the kavrynt-cloud ingestion spec. The runtime sends events
  unchanged, including `instance`, `seq`, `prev_hash`, and `hash`, so Cloud
  can verify and deduplicate them.

## Cloud contract

The runtime side, for the kavrynt-cloud ingestion spec:

- `POST <url>/v1/sync/audit-events`, `Authorization: Bearer <cluster
  token>`, `Content-Type: application/json`, body
  `{"events": [<kavrynt.audit.v1 event>, ...]}` of at most 500 events and
  1 MiB, events in `seq` order per `instance`.
- Any `2xx` means every event in the batch is stored or was already
  stored (same `instance` and `seq`). `429` and `503` may send
  `Retry-After`. Other answers are failures; the batch is retried.
