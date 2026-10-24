#!/usr/bin/env bash
# Tests the chart's alert rules (SPEC-0010): renders the PrometheusRule with
# every alert on, checks that each alert has a runbook in docs/runbooks and
# each runbook an alert (R07), and runs promtool's rule check and the unit
# tests in test/alerts (R08). promtool runs from the Prometheus image, pinned
# by digest, so no Go dependency is added.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
PROMTOOL_IMAGE="${PROMTOOL_IMAGE:-prom/prometheus:v3.15.0@sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e}"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

for command in helm docker sed grep sort comm; do
  command -v "${command}" >/dev/null 2>&1 || fail "required command not found: ${command}"
done

# The PrometheusRule's spec is a Prometheus rule file indented by two spaces.
helm template kavrynt "${ROOT_DIR}/charts/kavrynt" --namespace kavrynt-system \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.prometheusRule.runbookBaseURL=https://runbooks.test \
  --set operator.cloud.enabled=true \
  --set operator.cloud.url=https://cloud.example.com \
  --set operator.cloud.tokenSecret.name=kavrynt-cloud-token \
  --show-only templates/prometheusrule.yaml |
  sed -n '/^spec:/,$p' | sed -e '1d' -e 's/^  //' >"${WORK_DIR}/rules.yaml"
cp "${ROOT_DIR}/test/alerts/rules_test.yaml" "${WORK_DIR}/"

grep -o 'alert: Kavrynt[A-Za-z]*' "${WORK_DIR}/rules.yaml" | sed 's/^alert: //' | sort >"${WORK_DIR}/alerts.txt"
[ -s "${WORK_DIR}/alerts.txt" ] || fail "the chart rendered no alerts"
find "${ROOT_DIR}/docs/runbooks" -name 'Kavrynt*.md' -exec basename {} .md \; | sort >"${WORK_DIR}/runbooks.txt"
missing="$(comm -23 "${WORK_DIR}/alerts.txt" "${WORK_DIR}/runbooks.txt")"
[ -z "${missing}" ] || fail "alerts without a runbook in docs/runbooks: ${missing//$'\n'/, }"
orphaned="$(comm -13 "${WORK_DIR}/alerts.txt" "${WORK_DIR}/runbooks.txt")"
[ -z "${orphaned}" ] || fail "runbooks without an alert: ${orphaned//$'\n'/, }"
for runbook in "${ROOT_DIR}"/docs/runbooks/Kavrynt*.md; do
  for section in Meaning Impact Diagnosis Mitigation; do
    grep --quiet "^## ${section}\$" "${runbook}" || fail "$(basename "${runbook}") has no '## ${section}' section"
  done
done

docker run --rm --user "$(id -u):$(id -g)" --volume "${WORK_DIR}:/work:ro" --workdir /work \
  --entrypoint promtool "${PROMTOOL_IMAGE}" check rules rules.yaml
docker run --rm --user "$(id -u):$(id -g)" --volume "${WORK_DIR}:/work:ro" --workdir /work \
  --entrypoint promtool "${PROMTOOL_IMAGE}" test rules rules_test.yaml

printf 'alert rules: %s alerts, each with a runbook and passing promtool tests\n' "$(wc -l <"${WORK_DIR}/alerts.txt" | tr -d ' ')"
