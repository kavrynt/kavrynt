#!/usr/bin/env bash
# Tests the chart's alert notifications (SPEC-0022-R05): renders the
# AlertmanagerConfig for each example values file and for every receiver,
# converts it into an Alertmanager configuration the way the Prometheus
# Operator does (receivers named <namespace>/<name>/<receiver>, Secrets
# mounted as files, the config's route under a root route), and runs amtool
# from the Alertmanager image, pinned by digest: check-config, routes test
# (each severity reaches its receivers, another release's alerts reach
# none), and template render (the message templates parse with
# Alertmanager's functions). yq reads the rendered YAML from its pinned
# image; jq converts it.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
AMTOOL_IMAGE="${AMTOOL_IMAGE:-prom/alertmanager:v0.34.1@sha256:e9733bafb1bdef9b00e25a21f8f99dc26a22224bf16641ad754d1649f4c3357a}"
YQ_IMAGE="${YQ_IMAGE:-mikefarah/yq:4.53.6@sha256:cfc4eee658595834ef304eadb0c3ea721f3b7cb6404ad8b7cb909cc5b5145b23}"
NAMESPACE=kavrynt-system
PREFIX="${NAMESPACE}/kavrynt-notifications/"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

for command in helm docker jq; do
  command -v "${command}" >/dev/null 2>&1 || fail "required command not found: ${command}"
done

# run IMAGE ENTRYPOINT ARGS... runs a tool with the work directory at /work.
run() {
  local image="$1" entrypoint="$2"
  shift 2
  docker run --rm --user "$(id -u):$(id -g)" --volume "${WORK_DIR}:/work" --workdir /work \
    --entrypoint "${entrypoint}" "${image}" "$@"
}

# Prometheus Operator's conversion of the fields the chart uses.
read -r -d '' CONVERT <<'JQ' || true
def secret: "/work/secrets/\(.name)/\(.key)";
def route($prefix):
  {receiver: ($prefix + .receiver)}
  + (if .groupBy then {group_by: .groupBy} else {} end)
  + (if .groupWait then {group_wait: .groupWait} else {} end)
  + (if .groupInterval then {group_interval: .groupInterval} else {} end)
  + (if .repeatInterval then {repeat_interval: .repeatInterval} else {} end)
  + (if .matchers then {matchers: [.matchers[] | "\(.name)\(.matchType)\(.value | tojson)"]} else {} end)
  + (if .continue then {continue: true} else {} end)
  + (if .routes then {routes: [.routes[] | route($prefix)]} else {} end);
def receiver($prefix):
  {name: ($prefix + .name)}
  + (if .slackConfigs then {slack_configs: [.slackConfigs[] |
      {api_url_file: (.apiURL | secret), send_resolved: .sendResolved, title, text}
      + (if .channel then {channel} else {} end)]} else {} end)
  + (if .emailConfigs then {email_configs: [.emailConfigs[] |
      {to, send_resolved: .sendResolved, require_tls: .requireTLS, text, html,
       headers: (.headers | map({(.key): .value}) | add)}
      + (if .from then {from} else {} end)
      + (if .smarthost then {smarthost} else {} end)
      + (if .authUsername then {auth_username: .authUsername} else {} end)
      + (if .authPassword then {auth_password_file: (.authPassword | secret)} else {} end)]} else {} end)
  + (if .msteamsv2Configs then {msteamsv2_configs: [.msteamsv2Configs[] |
      {webhook_url_file: (.webhookURL | secret), send_resolved: .sendResolved, title, text}]} else {} end)
  + (if .webhookConfigs then {webhook_configs: [.webhookConfigs[] |
      {url_file: (.urlSecret | secret), send_resolved: .sendResolved}]} else {} end);
.spec as $spec
| {
    global: {smtp_smarthost: "smtp.test:25", smtp_from: "alertmanager@example.com"},
    route: {receiver: "none", routes: [($spec.route | route($prefix)) + {continue: true}]},
    receivers: ([{name: "none"}] + [$spec.receivers[] | receiver($prefix)])
  }
JQ

# check NAME HELM-ARGS... renders the config, writes its Secrets, and runs
# check-config.
check() {
  local name="$1"
  shift
  helm template kavrynt "${ROOT_DIR}/charts/kavrynt" --namespace "${NAMESPACE}" \
    --set gateway.auth.enabled=false "$@" \
    --show-only templates/alertmanagerconfig.yaml >"${WORK_DIR}/${name}.cr.yaml"
  run "${YQ_IMAGE}" yq -o=json '.' "/work/${name}.cr.yaml" >"${WORK_DIR}/${name}.cr.json"
  jq --arg prefix "${PREFIX}" "${CONVERT}" "${WORK_DIR}/${name}.cr.json" >"${WORK_DIR}/${name}.json"
  jq -r '.spec.receivers[] | (.slackConfigs[]?.apiURL, .emailConfigs[]?.authPassword, .msteamsv2Configs[]?.webhookURL,
    .webhookConfigs[]?.urlSecret) | select(.) | "\(.name)/\(.key)"' "${WORK_DIR}/${name}.cr.json" |
    while read -r secret; do
      mkdir -p "${WORK_DIR}/secrets/$(dirname "${secret}")"
      printf 'https://hooks.example.com/%s' "${secret}" >"${WORK_DIR}/secrets/${secret}"
    done
  run "${AMTOOL_IMAGE}" amtool check-config "/work/${name}.json" >/dev/null || fail "${name}: amtool rejected the configuration"
}

# expect NAME RECEIVERS LABELS... checks where an alert with LABELS goes.
expect() {
  local name="$1" receivers="$2"
  shift 2
  run "${AMTOOL_IMAGE}" amtool config routes test --config.file="/work/${name}.json" --verify.receivers="${receivers}" "$@" >/dev/null ||
    fail "${name}: an alert with $* did not reach ${receivers}"
}

check slack --values "${ROOT_DIR}/examples/helm/values-notifications-slack.yaml"
expect slack "${PREFIX}slack" alertname=KavryntMCPServerNotReady severity=warning component=mcp-server kavrynt_release=kavrynt namespace=team-a name=search
expect slack "${PREFIX}slack" alertname=KavryntGatewayDown severity=critical component=gateway kavrynt_release=kavrynt
expect slack none alertname=KavryntGatewayDown severity=critical component=gateway kavrynt_release=other
expect slack none alertname=Watchdog severity=none

check email --values "${ROOT_DIR}/examples/helm/values-notifications-email.yaml"
expect email "${PREFIX}email" alertname=KavryntOperatorDown severity=critical component=operator kavrynt_release=kavrynt

check all \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.notifications.enabled=true \
  --set metrics.notifications.slack.webhookSecret.name=alerts \
  --set 'metrics.notifications.slack.severities={warning}' \
  --set 'metrics.notifications.email.to={platform@example.com}' \
  --set metrics.notifications.msteams.webhookSecret.name=teams \
  --set metrics.notifications.webhook.urlSecret.name=pager \
  --set 'metrics.notifications.webhook.severities={critical}'
expect all "${PREFIX}slack,${PREFIX}email,${PREFIX}msteams" alertname=KavryntMCPToolErrorsHigh severity=warning component=mcp-server kavrynt_release=kavrynt route=team-a.search tool=lookup
expect all "${PREFIX}email,${PREFIX}msteams,${PREFIX}webhook" alertname=KavryntGatewayDown severity=critical component=gateway kavrynt_release=kavrynt

: >"${WORK_DIR}/none.tmpl"
for template in title text; do
  run "${AMTOOL_IMAGE}" amtool template render --template.glob=/work/none.tmpl \
    --template.text="$(cat "${ROOT_DIR}/charts/kavrynt/notifications/${template}.tmpl")" >/dev/null ||
    fail "Alertmanager cannot render notifications/${template}.tmpl"
done

printf 'notifications: Slack, email, Teams, and webhook configurations pass amtool\n'
