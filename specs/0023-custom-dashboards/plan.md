# SPEC-0023 plan: Custom dashboards

## Design

- **Server dashboard (R01).** `charts/kavrynt/charts/gateway/dashboards/mcp-server.json`,
  uid `kavrynt-mcp-server`, added to the existing dashboard ConfigMap as
  `kavrynt-mcp-server.json`. Variables: `datasource` (Prometheus), then
  `namespace` and `name` from `kavrynt_operator_mcpserver_ready`, so a
  server without traffic can be picked. Gateway queries select
  `route="$namespace.$name"`. Panels: Ready and tools discovered (stats from
  the Operator gauges, with value mappings), requests per second, error
  rate, requests by outcome, p50 and p95 latency by tool, top failing tools
  (`topk` of `tool_error`/`rpc_error` calls in the range), and bytes per
  second by direction.
- **Extra dashboards (R02).** `gateway.metrics.dashboard.extra` (default
  `{}`): the schema requires keys matching `^[a-z0-9][a-z0-9-]*\.json$` and
  string or object values. `dashboard.yaml` renders one ConfigMap
  `<fullname>-dashboard-<key without .json>` per entry, with the sidecar
  labels and namespace of the default one; strings are parsed with
  `fromJson` and the template fails when that reports an error, objects are
  written with `toPrettyJson`. Rendered only with
  `gateway.metrics.dashboard.enabled`.
- **Metric check (R03).** `test/dashboards/dashboards_test.go` finds the
  exported metric names by parsing the non-test Go files under `internal/`
  (`go/parser`, string literals `kavrynt_gateway_*` and
  `kavrynt_operator_*`), so the list cannot drift from the code. For every
  shipped dashboard it collects each `kavrynt_*` name in panel targets and
  variable queries (`_bucket`, `_sum`, and `_count` count as their
  histogram) and fails on names that are not exported; it also requires a
  `datasource` variable used by every panel and target. A test feeds the
  checker a dashboard that queries `kavrynt_gateway_unknown_metric` and
  expects that failure.
- **Metrics reference (R04).** A "Metrics reference" section in the Gateway
  and Operator component docs: name, type, labels and their bounds, and
  example PromQL (error rate per server, p95 per tool, calls per client
  from audit events instead of metrics, since metrics carry no identity).
  The same test fails when an exported metric is missing from the
  reference or the reference names one that is not exported.
- **Export guide (R05).** Chart README "Dashboards": export from Grafana
  ("Export", "Export for sharing externally" off), then
  `--set-file 'gateway.metrics.dashboard.extra.team\.json=team.json'` or a
  values file.

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/charts/gateway/dashboards/mcp-server.json` | New dashboard |
| `charts/kavrynt/charts/gateway/templates/dashboard.yaml` | Second key, extra ConfigMaps |
| `charts/kavrynt/charts/gateway/values.yaml`, `values.schema.json` | `metrics.dashboard.extra` |
| `charts/kavrynt/tests/dashboards_test.yaml` | Chart tests |
| `test/dashboards/dashboards_test.go` | Metric and reference checks |
| `docs/components/gateway/README.md`, `docs/components/operator/README.md` | Metrics reference |
| `charts/kavrynt/README.md`, `CLAUDE.md`, `ROADMAP.md` | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0023-R01 | `test/dashboards/dashboards_test.go` `TestServerDashboardPanels`; `charts/kavrynt/tests/dashboards_test.yaml` |
| SPEC-0023-R02 | `charts/kavrynt/tests/dashboards_test.yaml` |
| SPEC-0023-R03 | `test/dashboards/dashboards_test.go` |
| SPEC-0023-R04 | `test/dashboards/dashboards_test.go` `TestMetricsReference` |
| SPEC-0023-R05 | `test/dashboards/dashboards_test.go` `TestChartREADMEShowsExtraDashboards` |

## Rollout

Part of `gateway.metrics.dashboard.enabled` (off by default); the existing
dashboard is unchanged. Release notes: the server dashboard, `extra`, and
the metrics reference.

## Risks

- A metric name built at run time would be missed by the source scan and
  reported as not exported. That fails the test loudly; such a metric
  would need to be a literal.
- Large custom dashboards: a ConfigMap holds at most 1 MiB, which the API
  server enforces at install.
