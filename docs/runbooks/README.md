# Runbooks

One runbook per alert in the chart's PrometheusRule
(`metrics.prometheusRule.enabled`, [SPEC-0010](../../specs/0010-alerts-and-runbooks/spec.md),
[SPEC-0021](../../specs/0021-tool-and-discovery-alerts/spec.md)).
Every alert's `runbook_url` points here at the chart's release tag, and
`scripts/test-alerts.sh` fails when an alert and its runbook do not match.
With `metrics.notifications` ([SPEC-0022](../../specs/0022-alert-notifications/spec.md))
every Slack, email, and Teams message links its alert's runbook here.

| Alert | Severity | Component |
| --- | --- | --- |
| [KavryntGatewayDown](KavryntGatewayDown.md) | critical | gateway |
| [KavryntGatewayRouteSyncFailing](KavryntGatewayRouteSyncFailing.md) | critical | gateway |
| [KavryntMCPUpstreamErrorsHigh](KavryntMCPUpstreamErrorsHigh.md) | warning | mcp-server |
| [KavryntMCPLatencyHigh](KavryntMCPLatencyHigh.md) | warning | mcp-server |
| [KavryntMCPToolErrorsHigh](KavryntMCPToolErrorsHigh.md) | warning | mcp-server |
| [KavryntOperatorDown](KavryntOperatorDown.md) | critical | operator |
| [KavryntMCPServerNotReady](KavryntMCPServerNotReady.md) | warning | mcp-server |
| [KavryntMCPToolDiscoveryFailing](KavryntMCPToolDiscoveryFailing.md) | warning | mcp-server |
| [KavryntCloudSyncStale](KavryntCloudSyncStale.md) | warning | operator |

The commands assume release `kavrynt` in namespace `kavrynt-system`.
