#!/usr/bin/env bash
# Creates specs/NNNN-<name>/spec.md from the template with the next free
# number and adds it to the index in specs/README.md.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SPECS_DIR="${ROOT_DIR}/specs"

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

name="${1:-}"
[[ "${name}" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] ||
  fail "usage: make spec-new NAME=short-kebab-name"

last=0
for dir in "${SPECS_DIR}"/[0-9][0-9][0-9][0-9]-*/; do
  [ -d "${dir}" ] || continue
  number="$(basename "${dir}")"
  number=$((10#${number%%-*}))
  [ "${number}" -le "${last}" ] || last="${number}"
done
next="$(printf '%04d' $((last + 1)))"
dir="${SPECS_DIR}/${next}-${name}"

mkdir "${dir}"
sed -e "s/NNNN/${next}/g" -e "s/YYYY-MM-DD/$(date +%Y-%m-%d)/" \
  "${SPECS_DIR}/_templates/spec.md" >"${dir}/spec.md"
printf '| [%s](%s-%s/spec.md) | %s | Draft |\n' "${next}" "${next}" "${name}" "${name}" >>"${SPECS_DIR}/README.md"

printf 'created specs/%s-%s/spec.md\n' "${next}" "${name}"
printf 'set the title in spec.md and in the specs/README.md index; add plan.md from specs/_templates/ once Accepted\n'
