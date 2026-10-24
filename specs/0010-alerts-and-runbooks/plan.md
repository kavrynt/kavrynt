# SPEC-0010 plan: Alerts and runbooks

## Design

- **Readiness metric (R01).** A `GaugeVec` in the Operator controller
  package registered on controller-runtime's `metrics.Registry`. The
  reconciler sets it after computing the `Ready` condition and calls
  `DeleteLabelValues` when the object is gone (not found in reconcile).
  Runs on the leader only, like the reconciler; non-leaders export no
  series, so alerts use `max by (namespace, name)`.
- **Operator Service (R02).** `templates/service.yaml` (port `metrics`,
  from `config.metricsBindAddress`) and `templates/servicemonitor.yaml` in
  the Operator subchart.
- **Rules (R03–R06).** `charts/kavrynt/templates/prometheusrule.yaml` in the
  umbrella chart, with the rule groups in the helper `kavrynt.alertRules`
  (`templates/_alerts.tpl`). Expressions:
  - `KavryntGatewayDown`: `absent(up{job="<gateway svc>"} == 1)` for 5m, critical.
  - `KavryntGatewayRouteSyncFailing`: failures increased and successes did
    not over 10m, critical.
  - `KavryntMCPUpstreamErrorsHigh`: ratio of `upstream_error`,
    `upstream_unreachable`, `timeout` outcomes in
    `kavrynt_gateway_mcp_requests_total` per route over 10m above
    `errorRatio` (default 0.05), with a minimum request rate, warning.
  - `KavryntMCPLatencyHigh`: p95 of
    `kavrynt_gateway_mcp_request_duration_seconds` per route above
    `latencyP95Seconds` (default 5) for 15m, warning.
  - `KavryntOperatorDown`: `absent(up{job="<operator svc>"} == 1)` for 5m,
    critical.
  - `KavryntMCPServerNotReady`: `max by (namespace, name)
    (kavrynt_operator_mcpserver_ready) == 0` for `notReadyFor` (default
    10m), warning.
  - `KavryntCloudSyncStale`: `time() -
    kavrynt_operator_cloud_sync_last_success_timestamp_seconds > 600`, warning.
  `runbook_url`: `https://github.com/kavrynt/kavrynt/blob/v{{ .Chart.Version }}/docs/runbooks/<Alert>.md`.
  Job names default to the Service names from the subcharts'
  `fullnameOverride`; `metrics.prometheusRule.jobs` overrides them.
- **Runbooks (R07).** `docs/runbooks/<Alert>.md` plus `README.md` index.
  `scripts/test-alerts.sh` extracts alert names from the rendered rule
  (`helm template`, `grep`) and compares them with the files one to one,
  and checks each runbook's sections.
- **Rule tests (R08).** `test/alerts/rules_test.yaml` (promtool unit tests
  with synthetic series). `make alerts-test` renders the rule with all
  options on, extracts `spec`, runs `promtool check rules` and
  `promtool test rules` in `prom/prometheus@sha256:...` (digest pinned).
  Added to `make qa` and the QA workflow.

## Affected files

| Path | Change |
| --- | --- |
| `internal/operator/controller/` | Readiness gauge and tests |
| `charts/kavrynt/charts/k8s-operator/templates/service.yaml`, `servicemonitor.yaml` | New |
| `charts/kavrynt/templates/prometheusrule.yaml`, `_alerts.tpl` | New |
| Subchart `values.yaml` and schemas, umbrella `values.yaml` | New values |
| `charts/kavrynt/tests/` | Rule rendering, Service, ServiceMonitor cases |
| `docs/runbooks/` | New |
| `test/alerts/` | promtool unit tests |
| `scripts/test-alerts.sh`, `Makefile`, `.github/workflows/qa.yml` | New checks (`make alerts-test`, in `make qa`) |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0010-R01 | `internal/operator/controller/metrics_test.go` |
| SPEC-0010-R02 | `alerts_test.yaml` Service and ServiceMonitor |
| SPEC-0010-R03, R04, R05 | `alerts_test.yaml` rendered rules and annotations |
| SPEC-0010-R06 | `test/alerts/rules_test.yaml` alert label sets |
| SPEC-0010-R07 | `scripts/test-alerts.sh` via `make alerts-test` (cited in `test/alerts/rules_test.yaml`) |
| SPEC-0010-R08 | `test/alerts/rules_test.yaml` via `make alerts-test` |

## Rollout

Rules off by default; Operator metrics Service on. Release notes: new
alerts, runbooks, Operator metrics Service, readiness metric.

## Risks

- Job names differ when Prometheus discovers targets through annotations
  rather than ServiceMonitors. Mitigation: `metrics.prometheusRule.jobs`
  values override the job selectors.
