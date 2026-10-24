# Runbooks

One runbook per alert in the chart's PrometheusRule
(`metrics.prometheusRule.enabled`, [SPEC-0010](../../specs/0010-alerts-and-runbooks/spec.md)).
Every alert's `runbook_url` points here at the chart's release tag, and
`scripts/test-alerts.sh` fails when an alert and its runbook do not match.

| Alert | Severity | Component |
| --- | --- | --- |
| [KavryntGatewayDown](KavryntGatewayDown.md) | critical | gateway |
| [KavryntGatewayRouteSyncFailing](KavryntGatewayRouteSyncFailing.md) | critical | gateway |
| [KavryntMCPUpstreamErrorsHigh](KavryntMCPUpstreamErrorsHigh.md) | warning | mcp-server |
| [KavryntMCPLatencyHigh](KavryntMCPLatencyHigh.md) | warning | mcp-server |
| [KavryntOperatorDown](KavryntOperatorDown.md) | critical | operator |
| [KavryntMCPServerNotReady](KavryntMCPServerNotReady.md) | warning | mcp-server |
| [KavryntCloudSyncStale](KavryntCloudSyncStale.md) | warning | operator |

The commands assume release `kavrynt` in namespace `kavrynt-system`.
