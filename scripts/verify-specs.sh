#!/usr/bin/env bash
# Checks the spec-driven development contract described in specs/README.md:
# every spec is well formed and indexed, and every requirement of an
# Implemented spec is cited by at least one test.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SPECS_DIR="${ROOT_DIR}/specs"
REQ_PATTERN='SPEC-[0-9]{4}-R[0-9]{2}'

errors=0
error() {
  printf 'error: %s\n' "$*" >&2
  errors=$((errors + 1))
}

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT
: >"${work_dir}/numbers"
: >"${work_dir}/defined"
: >"${work_dir}/required"

for dir in "${SPECS_DIR}"/[0-9][0-9][0-9][0-9]-*/; do
  [ -d "${dir}" ] || continue
  name="$(basename "${dir}")"
  number="${name%%-*}"
  id="SPEC-${number}"
  spec="${dir}spec.md"
  printf '%s\n' "${number}" >>"${work_dir}/numbers"

  if [ ! -f "${spec}" ]; then
    error "specs/${name}: missing spec.md"
    continue
  fi
  head -n 1 "${spec}" | grep -Eq "^# ${id}: .+" ||
    error "specs/${name}/spec.md: first line must be '# ${id}: <title>'"

  status="$(awk -F '|' '$2 ~ /^ *Status *$/ { gsub(/ /, "", $3); print $3; exit }' "${spec}")"
  case "${status}" in
    Draft | Superseded | Withdrawn) ;;
    Accepted | Implemented)
      [ -f "${dir}plan.md" ] || error "specs/${name}: status ${status} requires plan.md"
      ;;
    *)
      error "specs/${name}/spec.md: status '${status}' is not Draft, Accepted, Implemented, Superseded, or Withdrawn"
      continue
      ;;
  esac

  grep -Fq "](${name}/spec.md) |" "${SPECS_DIR}/README.md" ||
    error "specs/${name} is missing from the index in specs/README.md"
  grep -F "](${name}/spec.md) |" "${SPECS_DIR}/README.md" | grep -Eq "\| ${status} \|$" ||
    error "specs/README.md index does not show specs/${name} as ${status}"

  definitions="$(grep -E "^- \*\*${REQ_PATTERN}\*\*" "${spec}" || true)"
  for req in $(printf '%s\n' "${definitions}" | grep -Eo "^- \*\*${REQ_PATTERN}" | grep -Eo "${REQ_PATTERN}" || true); do
    [ "${req%-R*}" = "${id}" ] || error "specs/${name}/spec.md defines ${req}, which belongs to another spec"
    printf '%s\n' "${req}" >>"${work_dir}/defined"
  done

  if [ "${status}" = "Implemented" ]; then
    active="$(printf '%s\n' "${definitions}" | grep -v 'Withdrawn' | grep -Eo "^- \*\*${REQ_PATTERN}" | grep -Eo "${REQ_PATTERN}" || true)"
    [ -n "${active}" ] || error "specs/${name}: Implemented spec defines no requirements"
    [ -z "${active}" ] || printf '%s\n' "${active}" >>"${work_dir}/required"
  fi
done

for number in $(sort "${work_dir}/numbers" | uniq -d); do
  error "spec number ${number} is used by more than one directory"
done
for req in $(sort "${work_dir}/defined" | uniq -d); do
  error "${req} is defined more than once"
done

# Citations: Go tests anywhere, chart unit tests, the end-to-end scripts, and
# everything under test/.
{
  find "${ROOT_DIR}" \( -path "${ROOT_DIR}/.git" -o -path "${ROOT_DIR}/.cache" -o -path "${ROOT_DIR}/.kavrynt" \) -prune \
    -o -type f -name '*_test.go' -print
  find "${ROOT_DIR}/charts" -type f -path '*/tests/*_test.yaml' -print
  find "${ROOT_DIR}/scripts" -type f -name 'e2e-*.sh' -print
  find "${ROOT_DIR}/test" -type f -print
} >"${work_dir}/sources"
while IFS= read -r source; do
  grep -Eoh "${REQ_PATTERN}" "${source}" || true
done <"${work_dir}/sources" | sort -u >"${work_dir}/cited"

while IFS= read -r req; do
  grep -Fqx "${req}" "${work_dir}/cited" || error "${req} is required by an Implemented spec but no test cites it"
done <"${work_dir}/required"
while IFS= read -r req; do
  grep -Fqx "${req}" "${work_dir}/defined" || error "a test cites ${req}, which no spec defines"
done <"${work_dir}/cited"

if [ "${errors}" -gt 0 ]; then
  printf 'spec check failed with %d error(s); see specs/README.md\n' "${errors}" >&2
  exit 1
fi
printf 'spec check passed: %d spec(s), %d cited requirement(s)\n' \
  "$(wc -l <"${work_dir}/numbers" | tr -d ' ')" "$(wc -l <"${work_dir}/cited" | tr -d ' ')"
