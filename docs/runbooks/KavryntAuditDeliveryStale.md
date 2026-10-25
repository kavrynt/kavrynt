# KavryntAuditDeliveryStale

## Meaning

A Gateway replica has audit events waiting for Kavrynt Cloud, and Cloud has
accepted no batch from it for longer than
`metrics.prometheusRule.auditDeliveryStaleSeconds` (default 600). Only
rendered when `gateway.audit.cloud.enabled` is true
([SPEC-0017](../../specs/0017-audit-delivery-to-cloud/spec.md)).

## Impact

Warning. MCP traffic is not affected, and the Gateway's standard output
still has every event. Kavrynt Cloud is missing this replica's recent
events. Once `gateway.audit.cloud.bufferSize` (100,000) events wait, the
oldest are dropped from delivery and counted in
`kavrynt_gateway_audit_delivery_dropped_total`; recover them from the
Gateway logs.

## Diagnosis

```bash
kubectl -n kavrynt-system logs <gateway pod named in the alert> --tail=200 | grep -i 'audit event delivery'
```

`kavrynt_gateway_audit_delivery_total{result}` counts attempts by result:

- `token`: the cluster token Secret (`gateway.audit.cloud.tokenSecret`) is
  missing or empty in the pod.
- `rejected`: Kavrynt Cloud refused the token (`401`, `403`) or the batch.
  Check that the token is current and allowed to send audit events.
- `throttled`, `server_error`: Kavrynt Cloud is busy or failing; delivery
  retries with backoff and honors `Retry-After`.
- `network`: DNS, a proxy, or a firewall blocks egress to
  `gateway.audit.cloud.url`, or the URL resolves to an address the Gateway
  refuses (link-local, cloud metadata, loopback).

`kavrynt_gateway_audit_delivery_buffer_length` shows how many events wait.

## Mitigation

Fix the token Secret (the Gateway reads it before every attempt, no restart
needed), the URL, or the network path. Delivery resumes with the oldest
waiting events, in order. If events were dropped, export them from the
Gateway logs (`"type":"kavrynt.audit.v1"` lines of that pod) and verify them
with `kavryctl audit verify`.
