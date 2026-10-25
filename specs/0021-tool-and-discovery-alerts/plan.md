# SPEC-0021 plan: Tool error and discovery alerts

## Design

- **Tools-discovered metric (R01).** A second `GaugeVec` next to
  `kavrynt_operator_mcpserver_ready` in the Operator controller package,
  registered on controller-runtime's `metrics.Registry`. The reconciler
  sets it from the `ToolsDiscovered` condition after `applyTools` (or the
  discovery-off branch): 1 when `True`, 0 for `DiscoveryFailed` and
  `CredentialUnavailable`, and it deletes the series when the condition is
  absent, for any other reason (`NotRoutable`, `CallerTokenRequired`), and
  in `forget` when the object is gone. Like the readiness metric it runs on
  the leader only, so alerts use `max by (namespace, name)`.
- **`KavryntMCPToolErrorsHigh` (R02).** In the `kavrynt-gateway` group of
  `kavrynt.alertRules` (`charts/kavrynt/templates/_alerts.tpl`): per
  `route` and `tool`, the rate of `kavrynt_gateway_mcp_requests_total`
  with `method="tools/call"` and `outcome=~"tool_error|rpc_error"` over the
  rate of all `tools/call` requests, above `toolErrorRatio`, and the
  `tools/call` rate at least `minToolRequestsPerSecond`; `for: 10m`,
  severity `warning`, component `mcp-server`. The Gateway already bounds
  the `tool` label per route (`maxToolsPerRoute`, overflow `other`).
- **`KavryntMCPToolDiscoveryFailing` (R03).** In the `kavrynt-operator`
  group: `max by (namespace, name)
  (kavrynt_operator_mcpserver_tools_discovered) == 0 and on (namespace,
  name) max by (namespace, name) (kavrynt_operator_mcpserver_ready) == 1`;
  `for: discoveryFailingFor` (default `15m`), severity `warning`, component
  `mcp-server`.
- **Annotations and runbooks (R04).** Same `summary`, `description`,
  `runbook_url`, and `alertLabels` handling as SPEC-0010-R04. Two runbooks
  in `docs/runbooks`; `scripts/test-alerts.sh` already requires one runbook
  per alert with the four sections.
- **Values.** `metrics.prometheusRule.toolErrorRatio` (0.2),
  `minToolRequestsPerSecond` (0.02), and `discoveryFailingFor` (`15m`) in
  the umbrella `values.yaml` and `values.schema.json`.

## Affected files

| Path | Change |
| --- | --- |
| `internal/operator/controller/metrics.go`, `mcpserver_controller.go` | Tools-discovered gauge |
| `internal/operator/controller/metrics_test.go` | Gauge tests |
| `charts/kavrynt/templates/_alerts.tpl` | Two rules |
| `charts/kavrynt/values.yaml`, `values.schema.json` | New values |
| `charts/kavrynt/tests/alerts_test.yaml`, `values_schema_test.yaml` | Rendering and schema cases |
| `test/alerts/rules_test.yaml` | promtool tests |
| `docs/runbooks/` | Two runbooks and the index |
| `charts/kavrynt/README.md`, `docs/components/operator/README.md` | Alert and values tables, metrics |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0021-R01 | `internal/operator/controller/metrics_test.go` |
| SPEC-0021-R02, R03 | `test/alerts/rules_test.yaml` via `make alerts-test`; `charts/kavrynt/tests/alerts_test.yaml` |
| SPEC-0021-R04 | `test/alerts/rules_test.yaml` annotations; `scripts/test-alerts.sh` runbook check |

## Rollout

Rules are part of `metrics.prometheusRule` (off by default); the metric is
always exported. Release notes: two new alerts with runbooks, the
tools-discovered metric, and three new values.

## Risks

- A tool whose errors are caller mistakes (bad arguments) raises
  `KavryntMCPToolErrorsHigh`. Mitigation: the runbook says how to tell
  this from a broken tool, and `toolErrorRatio` is tunable.
