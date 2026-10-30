# SPEC-0022 plan: Alert notifications

## Design

- **Release label (R01).** `kavrynt.alertRules` adds
  `kavrynt_release: <release name>` to the labels of every alert, before
  `alertLabels` (which cannot override it). The promtool tests expect it.
- **AlertmanagerConfig (R01, R03).** `charts/kavrynt/templates/alertmanagerconfig.yaml`
  renders `monitoring.coreos.com/v1alpha1` `AlertmanagerConfig`
  `<release>-notifications` in the release namespace when
  `metrics.notifications.enabled`. The template fails when
  `metrics.prometheusRule.enabled` is off or no receiver is configured. The
  top route matches `component=~"gateway|operator|mcp-server"` and
  `kavrynt_release="<release>"`, groups by `alertname`, `route`,
  `namespace`, and `name`, and takes `groupWait` (30s), `groupInterval`
  (5m), and `repeatInterval` (4h) from values. Its receiver is a `kavrynt-none`
  receiver with no configs. One child route per configured receiver matches
  `severity=~"<severities>"` (from the receiver's `severities`, default both)
  with `continue: true`, so each alert reaches every receiver that takes
  its severity: critical to a pager webhook and warning to Slack is one
  `severities` list each.
- **Receivers (R02).** Values under `metrics.notifications`:
  - `slack`: `webhookSecret` (`name`, `key`), `channel`;
  - `email`: `to` (list), `from`, `smarthost`, `authUsername`,
    `authPasswordSecret`, `requireTLS` (true); an empty `smarthost` and
    `from` use the Alertmanager's global SMTP settings;
  - `msteams`: `webhookSecret` (`msteamsv2Configs`);
  - `webhook`: `urlSecret`.
  A receiver is configured when its Secret name (or for email, `to`) is set.
  Secrets are `SecretKeySelector`s in the release namespace; the schema has
  no field that takes a URL or password.
- **Messages (R04).** One title and one text template in
  `charts/kavrynt/notifications/` (read with `.Files.Get`, so Helm does not
  interpret their Go template syntax) are used by Slack, Teams, and email
  (subject header, `text`, and `html` inside `<pre>`; Alertmanager's
  `html/template` escapes it). Per alert: name, severity, firing or resolved,
  the server (`route`, or `namespace/name`) and `tool` when present, summary,
  description, and runbook link. The webhook sends Alertmanager's standard
  JSON payload, which carries the same labels and annotations.
- **Validation (R05).** `scripts/test-notifications.sh`, run by
  `make alerts-test` (already in CI and `make qa`), renders the
  `AlertmanagerConfig` for each example values file and one with every
  receiver, converts it to an Alertmanager configuration the way the
  Prometheus Operator does (camelCase to snake_case, Secrets to `*_file`
  paths, the config's route under a root route), and runs, from the pinned
  Alertmanager image, `amtool check-config`, `amtool config routes test`
  (critical and warning alerts reach the expected receivers, another
  release's do not), and `amtool template render` on a sample firing
  `KavryntMCPServerNotReady` and `KavryntMCPToolErrorsHigh` (the message
  names the server, the tool, and the runbook). YAML is read with `yq` from
  its image pinned by digest; `jq` does the conversion.
- **Examples (R06).** `examples/helm/values-notifications-slack.yaml` and
  `values-notifications-email.yaml`, used by the chart tests and the script.

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/templates/_alerts.tpl` | `kavrynt_release` label |
| `charts/kavrynt/templates/alertmanagerconfig.yaml`, `_notifications.tpl` | The config |
| `charts/kavrynt/notifications/` | Message templates |
| `charts/kavrynt/values.yaml`, `values.schema.json` | `metrics.notifications` |
| `charts/kavrynt/tests/notifications_test.yaml`, `alerts_test.yaml`, `values_schema_test.yaml` | Chart tests |
| `test/alerts/rules_test.yaml` | Expected labels |
| `scripts/test-notifications.sh`, `Makefile` | amtool validation |
| `examples/helm/` | Two example values files |
| `charts/kavrynt/README.md`, `docs/runbooks/README.md`, `CLAUDE.md`, `ROADMAP.md` | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0022-R01 | `charts/kavrynt/tests/notifications_test.yaml`; `alerts_test.yaml` (label); `scripts/test-notifications.sh` routes test |
| SPEC-0022-R02 | `charts/kavrynt/tests/notifications_test.yaml` (each receiver); `values_schema_test.yaml` (no inline secrets) |
| SPEC-0022-R03 | `charts/kavrynt/tests/notifications_test.yaml`; `scripts/test-notifications.sh` routes test |
| SPEC-0022-R04 | `scripts/test-notifications.sh` template render |
| SPEC-0022-R05 | `charts/kavrynt/tests/notifications_test.yaml`; `scripts/test-notifications.sh` |
| SPEC-0022-R06 | `charts/kavrynt/tests/notifications_test.yaml` (example files) |

## Rollout

Off by default (`metrics.notifications.enabled`). The `kavrynt_release`
label appears on every alert after the upgrade. Release notes: notifications
to Slack, email, Teams, and webhooks, the new label, and the Alertmanager
matcher strategy it needs.

## Risks

- An Alertmanager with the default `OnNamespace` matcher strategy adds
  `namespace="<release namespace>"` to the route, so only some alerts are
  sent. Mitigation: the chart README and NOTES say to use `None`.
- Field names of `AlertmanagerConfig` drift between Prometheus Operator
  versions. Mitigation: only v1alpha1 fields are used, checked against the
  v0.94 CRD; Teams needs v0.82 or later, which the README says.
