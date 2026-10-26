# SPEC-0017 plan: Audit delivery to Kavrynt Cloud

## Design

- **Hand-off (R02).** `audit.Writer` keeps writing every event to standard
  output first; with a sink set (`SetSink`), it then passes the written
  line, with its `seq` and hash, to the sink. The sink never blocks the
  writer, so delivery adds no back-pressure beyond SPEC-0013's queue.
- **Shipper (R01, R03, R04).** `internal/gateway/audit/cloud.go`:
  - A bounded FIFO of encoded events (`--audit-cloud-buffer-size`, default
    100,000). When it is full, the oldest undelivered event is dropped and
    counted; standard output still has it.
  - A loop sends the oldest events as one batch when 500 are waiting, 1 MiB
    is waiting, or 10 seconds have passed: `POST <url>/v1/sync/audit-events`
    with `{"events": [...]}`, at most 500 events and 1 MiB, the cluster
    token read from the mounted file before each attempt.
  - A `2xx` removes the batch's events from the buffer (events dropped by
    overflow while it was in flight are not counted twice). Anything else
    retries the same events with exponential backoff and jitter (1 s up to
    5 min), or `Retry-After` on `429` and `503` (at most 1 h). Since events
    leave the buffer only when accepted, a retried batch can repeat events
    Cloud already stored; `instance` and `seq` identify them.
  - On shutdown, after the writer has written its queue, one last attempt
    within the shutdown timeout.
- **Metrics (R05).** `kavrynt_gateway_audit_delivery_total{result}`
  (`delivered`, `throttled`, `rejected`, `server_error`, `network`,
  `token`), `kavrynt_gateway_audit_delivery_buffer_length`,
  `kavrynt_gateway_audit_delivery_dropped_total`, and
  `kavrynt_gateway_audit_delivery_last_success_timestamp_seconds` (the
  Gateway's start time until the first accepted batch).
- **Alert (R05).** `KavryntAuditDeliveryStale` in the `kavrynt-gateway`
  group, only with `gateway.audit.cloud.enabled`: per instance, events are
  waiting and the last accepted batch is older than
  `metrics.prometheusRule.auditDeliveryStaleSeconds` (600), for 5 minutes;
  `warning`, component `gateway`. Runbook
  `docs/runbooks/KavryntAuditDeliveryStale.md`.
- **Security (R06).** The HTTP client uses `egress.Transport` (no
  link-local, metadata, or loopback addresses), follows no redirects, and
  times out after 30 s. The URL must be `https` without credentials, query,
  or fragment. The token is only ever put in the `Authorization` header;
  errors and logs never include it or Cloud's answer body.
- **Flags.** `--audit-cloud-url`, `--audit-cloud-token-file`,
  `--audit-cloud-buffer-size`; `--audit-cloud-url` needs `--audit`.
- **Chart.** Gateway values `audit.cloud.enabled`, `url`, `tokenSecret`
  (`name`, `key`), `bufferSize`. The Deployment fails to render without
  `audit.enabled`, an `https` URL, or a Secret name, mounts the Secret key
  as `/etc/kavrynt/audit-cloud/token` (no RBAC), and passes the flags.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/audit/writer.go`, `cloud.go` | Sink, shipper |
| `internal/gateway/audit/cloud_test.go`, `writer_test.go` | Tests |
| `internal/gateway/api/api.go` | Register the shipper's metrics |
| `cmd/gateway/main.go` | Flags, start and stop |
| `charts/kavrynt/charts/gateway/values.yaml`, `values.schema.json`, `templates/deployment.yaml` | Values and mount |
| `charts/kavrynt/templates/_alerts.tpl`, `values.yaml`, `values.schema.json` | Alert and its threshold |
| `charts/kavrynt/tests/audit_test.yaml`, `alerts_test.yaml` | Chart tests |
| `test/alerts/rules_test.yaml`, `scripts/test-alerts.sh` | promtool tests |
| `docs/runbooks/` | Runbook and index |
| `docs/components/gateway/README.md`, `charts/kavrynt/README.md`, `README.md`, `CLAUDE.md`, `ROADMAP.md`, `docs/ADR-0003-Runtime-Authorization.md` | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0017-R01 | `internal/gateway/audit/cloud_test.go` (batch limits, interval, token); `charts/kavrynt/tests/audit_test.yaml` |
| SPEC-0017-R02 | `internal/gateway/audit/cloud_test.go` `TestDeliveryRecoversWithoutGaps` (standard output and delivery) |
| SPEC-0017-R03 | `cloud_test.go` (503 for a while, then no gap in `seq`; `Retry-After`) |
| SPEC-0017-R04 | `cloud_test.go` (full buffer drops the oldest, MCP traffic not blocked) |
| SPEC-0017-R05 | `cloud_test.go` (metrics); `test/alerts/rules_test.yaml`; `scripts/test-alerts.sh` runbook check |
| SPEC-0017-R06 | `cloud_test.go` (no token in errors, no redirects, URL checks); `cmd/gateway` test (egress transport) |

## Rollout

Off by default (`gateway.audit.cloud.enabled`). It needs the Cloud
endpoint above; until kavrynt-cloud serves it, batches fail with
`rejected` and the alert fires, while standard output is unaffected.
Release notes: what is sent (every audit event, including the caller's
subject and client ID), the new values, metrics, and alert.

## Risks

- Cloud unavailable for long: the buffer holds 100,000 events (about
  50 MB at 500 bytes an event) and then drops the oldest; the alert
  fires after 15 minutes and standard output keeps every event.
- Personal data leaves the cluster: opt-in, documented in the Gateway docs
  and chart README.
