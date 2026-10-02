#!/usr/bin/env bash
# Upgrade test: install the runtime from FROM_REF (default: the last commit
# that shipped the in-cluster Registry), register an MCPServer, upgrade to the
# working tree, and verify routing, status migration, and deletion.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# d95ae17 is the develop merge of the runtime monorepo (0.0.1-beta.1 runtime).
FROM_REF="${FROM_REF:-d95ae17}"
CLUSTER_NAME="${KIND_CLUSTER_NAME:-kavrynt-upgrade}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
CONTROL_NAMESPACE="kavrynt-system"
E2E_NAMESPACE="kavrynt-e2e"
FROM_TAG="upgrade-from"
TO_TAG="upgrade-to"
SAMPLE_IMAGE="kavrynt/e2e-mcp-server:${TO_TAG}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

TEMP_DIR="$(mktemp -d)"
FROM_DIR="${TEMP_DIR}/from"
CREATED_CLUSTER=0
PORT_FORWARD_PID=""

log() { printf '==> %s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
kc() { kubectl --context "${KUBE_CONTEXT}" "$@"; }

stop_port_forward() {
  if [ -n "${PORT_FORWARD_PID}" ]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
    wait "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
    PORT_FORWARD_PID=""
  fi
}

cleanup() {
  local exit_code=$?
  set +e
  if [ "${exit_code}" -ne 0 ] && [ "${CREATED_CLUSTER}" -eq 1 ]; then
    log "collecting failure diagnostics"
    kc get pods -A -o wide
    kc -n "${E2E_NAMESPACE}" get mcpservers -o yaml
    kc -n "${CONTROL_NAMESPACE}" logs deployment/kavrynt-operator --tail=100
    kc -n "${CONTROL_NAMESPACE}" logs deployment/kavrynt-gateway --tail=100
  fi
  stop_port_forward
  rm -rf "${TEMP_DIR}"
  if [ "${CREATED_CLUSTER}" -eq 1 ] && [ "${KEEP_CLUSTER}" != "1" ]; then
    kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

wait_for_json() {
  local url=$1 expression=$2 attempts=${3:-60} i
  for ((i = 1; i <= attempts; i++)); do
    if curl --fail --silent "${url}" | jq --exit-status "${expression}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${expression} at ${url}"
}

start_gateway_port_forward() {
  kc -n "${CONTROL_NAMESPACE}" port-forward service/kavrynt-gateway 18090:8080 \
    >"${TEMP_DIR}/gateway-port-forward.log" 2>&1 &
  PORT_FORWARD_PID=$!
  wait_for_json "http://127.0.0.1:18090/readyz" '.status == "ready"'
}

call_tools_list() {
  curl --fail --silent --show-error \
    --request POST "http://127.0.0.1:18090/mcp/${E2E_NAMESPACE}.example-mcp-server" \
    --header 'Content-Type: application/json' \
    --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' |
    jq --exit-status '.result.tools[0].name == "echo"' >/dev/null
}

for command in docker kind kubectl helm curl jq git tar; do
  command -v "${command}" >/dev/null 2>&1 || fail "required command not found: ${command}"
done
docker info >/dev/null 2>&1 || fail "Docker is not reachable"
if kind get clusters | grep --fixed-strings --line-regexp "${CLUSTER_NAME}" >/dev/null 2>&1; then
  fail "Kind cluster ${CLUSTER_NAME} already exists; delete it or set KIND_CLUSTER_NAME"
fi

log "exporting ${FROM_REF} for the starting release"
mkdir -p "${FROM_DIR}"
git -C "${ROOT_DIR}" archive "${FROM_REF}" | tar -x -C "${FROM_DIR}"

log "building starting images from ${FROM_REF}"
for target in registry gateway operator; do
  docker build --quiet --file "${FROM_DIR}/build/Dockerfile" --target "${target}" \
    --tag "kavrynt/${target}:${FROM_TAG}" "${FROM_DIR}" >/dev/null
done

log "building upgrade images from the working tree"
for target in gateway operator; do
  docker build --quiet --file "${ROOT_DIR}/build/Dockerfile" --target "${target}" \
    --tag "kavrynt/${target}:${TO_TAG}" "${ROOT_DIR}" >/dev/null
done
docker build --quiet --tag "${SAMPLE_IMAGE}" "${ROOT_DIR}/test/e2e/mcp-server" >/dev/null

log "creating Kind cluster ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --wait 120s
CREATED_CLUSTER=1
kind load docker-image --name "${CLUSTER_NAME}" \
  "kavrynt/registry:${FROM_TAG}" "kavrynt/gateway:${FROM_TAG}" "kavrynt/operator:${FROM_TAG}" \
  "kavrynt/gateway:${TO_TAG}" "kavrynt/operator:${TO_TAG}" "${SAMPLE_IMAGE}"

log "installing the starting release"
helm install kavrynt "${FROM_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" --create-namespace \
  --set-string registry.image.tag="${FROM_TAG}" \
  --set-string gateway.image.tag="${FROM_TAG}" \
  --set-string operator.image.tag="${FROM_TAG}" \
  --wait --timeout 180s

sed "s|__SAMPLE_IMAGE__|${SAMPLE_IMAGE}|g" "${ROOT_DIR}/test/e2e/manifests/mcp-server.yaml" >"${TEMP_DIR}/mcp-server.yaml"
kc apply -f "${TEMP_DIR}/mcp-server.yaml"
kc -n "${E2E_NAMESPACE}" rollout status deployment/example-mcp-server --timeout=120s
kc -n "${E2E_NAMESPACE}" wait mcpserver/example-mcp-server --for=condition=Registered --timeout=120s
kc -n "${E2E_NAMESPACE}" get mcpserver/example-mcp-server -o json |
  jq --exit-status '.metadata.finalizers | index("mcpservers.kavrynt.io/registry-sync") != null' >/dev/null ||
  fail "starting release did not add the legacy finalizer"

start_gateway_port_forward
wait_for_json "http://127.0.0.1:18090/v1/routes" '.routes | length == 1'
call_tools_list
stop_port_forward

log "upgrading: CRD first, then the chart"
kc apply --server-side --force-conflicts -f "${ROOT_DIR}/charts/kavrynt/charts/k8s-operator/crds/"
helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" \
  --set-string gateway.image.tag="${TO_TAG}" \
  --set-string operator.image.tag="${TO_TAG}" \
  --wait --timeout 180s

for deployment in kavrynt-gateway kavrynt-operator; do
  kc -n "${CONTROL_NAMESPACE}" rollout status "deployment/${deployment}" --timeout=120s
done
if kc -n "${CONTROL_NAMESPACE}" get deployment/kavrynt-registry >/dev/null 2>&1; then
  fail "Registry deployment still exists after upgrade"
fi

log "verifying MCPServer status migration"
kc -n "${E2E_NAMESPACE}" wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s
kc -n "${E2E_NAMESPACE}" get mcpserver/example-mcp-server -o json | jq --exit-status '
  ((.metadata.finalizers // []) | length == 0) and
  (.status.conditions | map(.type) | index("Registered") == null) and
  (.status.registrySyncedAt == null)' >/dev/null ||
  fail "legacy finalizer, condition, or status fields were not migrated"

log "verifying routing after upgrade"
start_gateway_port_forward
wait_for_json "http://127.0.0.1:18090/v1/routes" '.routes | length == 1'
call_tools_list

log "verifying deletion does not hang"
kc -n "${E2E_NAMESPACE}" delete mcpserver/example-mcp-server --wait=true --timeout=60s
wait_for_json "http://127.0.0.1:18090/v1/routes" '.routes | length == 0' 30

log "upgrade from ${FROM_REF} passed"
