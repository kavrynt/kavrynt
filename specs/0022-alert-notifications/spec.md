# SPEC-0022: Alert notifications

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

The chart can install an `AlertmanagerConfig` that sends Kavrynt alerts to
Slack, email, Microsoft Teams, or a webhook (for example PagerDuty through
its webhook bridge), with messages that name the server or tool, say what
is wrong, and link the runbook. A tested example covers the common setups.

## Problem

The chart installs alert rules (SPEC-0010), but routing them to people is left
to each team's Alertmanager configuration, which is error-prone and has no
example. Teams install alerts and never hear from them.

## Goals

- One Helm value block gets Kavrynt alerts to a Slack channel, an email list,
  a Teams channel, or a webhook.
- Messages are useful without opening Prometheus.

## Non-goals

- Running Alertmanager (the Prometheus Operator provides it).
- Notifications from the console's own alerts (SPEC-0020) without
  Alertmanager.

## Requirements

- **SPEC-0022-R01** With `metrics.notifications.enabled=true` (requires
  `metrics.prometheusRule.enabled`), the chart MUST render one
  `AlertmanagerConfig` (monitoring.coreos.com/v1alpha1) that routes alerts
  with `component` in `gateway`, `operator`, or `mcp-server` from this
  release to the configured receivers. Every chart alert MUST carry the
  label `kavrynt_release` (the release name) so the route can tell this
  release's alerts from another's.
- **SPEC-0022-R02** Receivers MUST support Slack (webhook URL from a Secret),
  email (addresses; SMTP settings from Alertmanager or values), Microsoft
  Teams (Alertmanager's `msteamsv2` receiver, webhook URL from a Secret), and
  a generic webhook (URL from a Secret); secrets MUST be referenced, never
  put in values.
- **SPEC-0022-R03** `critical` and `warning` alerts MUST be routable to
  different receivers, with grouping by alert name and route (or
  `namespace` and `name` for alerts about an `MCPServer`) and a
  configurable repeat interval (default 4 hours).
- **SPEC-0022-R04** Messages MUST include the alert name, severity, the
  server (and tool when present), the summary and description, firing or
  resolved state, and the runbook link.
- **SPEC-0022-R05** Chart tests MUST cover each receiver type, and `amtool
  check-config` (from the pinned Alertmanager image) MUST validate the
  rendered routing in CI.
- **SPEC-0022-R06** The repository MUST ship an example values file for
  Slack and one for email, used by the tests.

## Acceptance criteria

- Given `metrics.notifications.slack.webhookSecret: {name: alerts, key: url}`,
  then the rendered `AlertmanagerConfig` sends Kavrynt alerts to that webhook
  and `amtool` accepts it (R01, R02, R05).
- Given `KavryntMCPServerNotReady` fires, then the Slack message names the
  server and links its runbook (R04).

## Security and privacy

Webhook URLs and SMTP passwords are secrets: referenced from Secrets only.
Alert content is route, tool, and server names, no arguments or identities.

## Compatibility

New values under `metrics.notifications`; needs the Prometheus Operator's
`AlertmanagerConfig` CRD and an Alertmanager that selects it. Alerts about an
`MCPServer` carry that server's `namespace`, not the release's, so the
Alertmanager must not scope the config to its own namespace
(`alertmanagerConfigMatcherStrategy.type: None`). The new `kavrynt_release`
label is added to every alert; Alertmanager routes that match on labels
keep working.

## Documentation

Chart README (notifications), runbooks README, examples.

## Decisions

- Microsoft Teams gets a dedicated receiver (`msteamsv2`, Teams workflows),
  because its message card needs a title and text that the generic webhook
  cannot shape.
