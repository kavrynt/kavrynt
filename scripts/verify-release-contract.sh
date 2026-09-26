#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_ROOT="${RUNTIME_ROOT:-${ROOT_DIR}/..}"

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

chart_field() {
  local chart=$1
  local field=$2
  awk -v field="${field}" '$1 == field ":" { print $2; exit }' "${chart}"
}

UMBRELLA_CHART="${ROOT_DIR}/charts/kavrynt/Chart.yaml"
KAVRYCTL_RELEASE_WORKFLOW="${RUNTIME_ROOT}/kavryctl/.github/workflows/release.yml"
VERSION="$(chart_field "${UMBRELLA_CHART}" version)"
APP_VERSION="$(chart_field "${UMBRELLA_CHART}" appVersion)"

[ -n "${VERSION}" ] || fail "umbrella chart version is empty"
[ "${VERSION}" = "${APP_VERSION}" ] || fail "umbrella chart version and appVersion differ"

for component in registry gateway operator; do
  case "${component}" in
    operator) chart="${RUNTIME_ROOT}/operator/charts/k8s-operator/Chart.yaml" ;;
    *) chart="${RUNTIME_ROOT}/${component}/charts/${component}/Chart.yaml" ;;
  esac
  [ -f "${chart}" ] || fail "canonical ${component} chart not found: ${chart}"
  [ "$(chart_field "${chart}" version)" = "${VERSION}" ] ||
    fail "${component} chart version does not match ${VERSION}"
  [ "$(chart_field "${chart}" appVersion)" = "${VERSION}" ] ||
    fail "${component} chart appVersion does not match ${VERSION}"
done

[ "$(grep --count --fixed-strings "tag: ${VERSION}" "${ROOT_DIR}/charts/kavrynt/values.yaml")" -eq 3 ] ||
  fail "umbrella image tags do not all match ${VERSION}"

for component in registry gateway operator; do
  grep --quiet --fixed-strings "repository: kavrynt/${component}" "${ROOT_DIR}/charts/kavrynt/values.yaml" ||
    fail "umbrella values do not use kavrynt/${component}"
done

grep --quiet --fixed-strings 'kavrynt/kavryctl' "${ROOT_DIR}/scripts/install.sh" ||
  fail "Unix installer does not target the canonical kavryctl release"
grep --quiet --fixed-strings 'kavrynt/kavryctl' "${ROOT_DIR}/scripts/install.ps1" ||
  fail "PowerShell installer does not target the canonical kavryctl release"
grep --quiet --fixed-strings "v${VERSION}" "${ROOT_DIR}/scripts/install.sh" ||
  fail "Unix installer version does not match ${VERSION}"
grep --quiet --fixed-strings "v${VERSION}" "${ROOT_DIR}/scripts/install.ps1" ||
  fail "PowerShell installer version does not match ${VERSION}"

[ -f "${KAVRYCTL_RELEASE_WORKFLOW}" ] ||
  fail "canonical kavryctl release workflow not found"
grep --quiet --fixed-strings 'name="kavryctl_${version}_${GOOS}_${GOARCH}"' "${KAVRYCTL_RELEASE_WORKFLOW}" ||
  fail "kavryctl release archive naming contract changed"
grep --quiet --fixed-strings 'archive_dir="kavryctl_${version}_${os}_${arch}"' "${ROOT_DIR}/scripts/install.sh" ||
  fail "Unix installer archive naming contract changed"
grep --quiet --fixed-strings '$archiveDir = "kavryctl_${releaseVersion}_windows_${arch}"' "${ROOT_DIR}/scripts/install.ps1" ||
  fail "PowerShell installer archive naming contract changed"
for file in "${KAVRYCTL_RELEASE_WORKFLOW}" "${ROOT_DIR}/scripts/install.sh" "${ROOT_DIR}/scripts/install.ps1"; do
  grep --quiet --fixed-strings 'SHA256SUMS' "${file}" ||
    fail "checksum filename contract changed in ${file}"
done

bash -n \
  "${ROOT_DIR}/scripts/e2e-kind.sh" \
  "${ROOT_DIR}/scripts/e2e-release.sh" \
  "${ROOT_DIR}/scripts/install.sh" \
  "${ROOT_DIR}/scripts/verify-release-contract.sh"

printf 'release contract %s is internally consistent\n' "${VERSION}"
