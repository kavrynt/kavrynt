#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_ROOT="${RUNTIME_ROOT:-${ROOT_DIR}/..}"
KAVRYCTL_REPO="${KAVRYCTL_REPO:-${RUNTIME_ROOT}/kavryctl}"
REGISTRY_REPO="${REGISTRY_REPO:-${RUNTIME_ROOT}/registry}"
GATEWAY_REPO="${GATEWAY_REPO:-${RUNTIME_ROOT}/gateway}"
OPERATOR_REPO="${OPERATOR_REPO:-${RUNTIME_ROOT}/k8s-operator}"
CLUSTER_NAME="${KIND_CLUSTER_NAME:-kavrynt-alpha0}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
CONTROL_NAMESPACE="kavrynt-system"
E2E_NAMESPACE="kavrynt-e2e"
LOCAL_VERSION="${LOCAL_VERSION:-0.0.1-beta-local}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

REGISTRY_IMAGE="kavrynt/registry:${LOCAL_VERSION}"
GATEWAY_IMAGE="kavrynt/gateway:${LOCAL_VERSION}"
OPERATOR_IMAGE="kavrynt/operator:${LOCAL_VERSION}"
SAMPLE_IMAGE="kavrynt/e2e-mcp-server:${LOCAL_VERSION}"

TEMP_DIR="$(mktemp -d)"
CREATED_CLUSTER=0
PORT_FORWARD_PIDS=()

log() {
  printf '==> %s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

git_commit() {
  git -C "$1" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown'
}

diagnostics() {
  log "collecting failure diagnostics"
  kubectl --context "${KUBE_CONTEXT}" get nodes -o wide || true
  kubectl --context "${KUBE_CONTEXT}" get pods -A -o wide || true
  kubectl --context "${KUBE_CONTEXT}" get events -A --sort-by=.lastTimestamp || true
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs deployment/kavrynt-registry --tail=100 || true
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs deployment/kavrynt-gateway --tail=100 || true
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs deployment/kavrynt-operator --tail=100 || true
}

cleanup() {
  local exit_code=$?
  set +e

  if [ "${exit_code}" -ne 0 ] && [ "${CREATED_CLUSTER}" -eq 1 ]; then
    diagnostics
  fi

  if [ "${#PORT_FORWARD_PIDS[@]}" -gt 0 ]; then
    kill "${PORT_FORWARD_PIDS[@]}" >/dev/null 2>&1 || true
    wait "${PORT_FORWARD_PIDS[@]}" >/dev/null 2>&1 || true
  fi

  rm -rf "${TEMP_DIR}"

  if [ "${CREATED_CLUSTER}" -eq 1 ] && [ "${KEEP_CLUSTER}" != "1" ]; then
    kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
  elif [ "${CREATED_CLUSTER}" -eq 1 ]; then
    log "keeping Kind cluster ${CLUSTER_NAME}"
  fi

  exit "${exit_code}"
}
trap cleanup EXIT

wait_for_url() {
  local url=$1
  local attempts=${2:-60}
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl --fail --silent --show-error "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${url}"
}

wait_for_json() {
  local url=$1
  local expression=$2
  local attempts=${3:-60}
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl --fail --silent "${url}" | jq --exit-status "${expression}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for ${expression} at ${url}"
}

for command in docker kind kubectl helm curl jq git sed; do
  require_command "${command}"
done

for repository in "${KAVRYCTL_REPO}" "${REGISTRY_REPO}" "${GATEWAY_REPO}" "${OPERATOR_REPO}"; do
  [ -d "${repository}" ] || fail "runtime repository not found: ${repository}"
done

docker info >/dev/null 2>&1 || fail "Docker is not reachable"

if kind get clusters | grep --fixed-strings --line-regexp "${CLUSTER_NAME}" >/dev/null 2>&1; then
  fail "Kind cluster ${CLUSTER_NAME} already exists; delete it or set KIND_CLUSTER_NAME"
fi

BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

log "building canonical kavryctl client"
(
  cd "${KAVRYCTL_REPO}"
  GOWORK=off GOCACHE="${TEMP_DIR}/go-build" go build -o "${TEMP_DIR}/kavryctl" .
)

log "building canonical runtime images"
docker build \
  --build-arg VERSION=0.0.1-beta \
  --build-arg COMMIT="$(git_commit "${REGISTRY_REPO}")" \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  --tag "${REGISTRY_IMAGE}" "${REGISTRY_REPO}"
docker build \
  --build-arg VERSION=0.0.1-beta \
  --build-arg COMMIT="$(git_commit "${GATEWAY_REPO}")" \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  --tag "${GATEWAY_IMAGE}" "${GATEWAY_REPO}"
docker build \
  --build-arg VERSION=0.0.1-beta \
  --build-arg COMMIT="$(git_commit "${OPERATOR_REPO}")" \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  --tag "${OPERATOR_IMAGE}" "${OPERATOR_REPO}"
docker build --tag "${SAMPLE_IMAGE}" "${ROOT_DIR}/test/e2e/mcp-server"

log "creating Kind cluster ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --wait 120s
CREATED_CLUSTER=1

log "loading local images"
kind load docker-image --name "${CLUSTER_NAME}" \
  "${REGISTRY_IMAGE}" "${GATEWAY_IMAGE}" "${OPERATOR_IMAGE}" "${SAMPLE_IMAGE}"

log "building umbrella chart dependencies from canonical repositories"
helm dependency build "${ROOT_DIR}/charts/kavrynt"

log "installing Kavrynt control plane"
helm upgrade --install kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --create-namespace \
  --set registry.image.repository=kavrynt/registry \
  --set-string registry.image.tag="${LOCAL_VERSION}" \
  --set gateway.image.repository=kavrynt/gateway \
  --set-string gateway.image.tag="${LOCAL_VERSION}" \
  --set operator.image.repository=kavrynt/operator \
  --set-string operator.image.tag="${LOCAL_VERSION}" \
  --wait \
  --timeout 180s

for deployment in kavrynt-registry kavrynt-gateway kavrynt-operator; do
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
    rollout status "deployment/${deployment}" --timeout=120s
done

log "deploying the Alpha-0 MCP server"
sed "s|__SAMPLE_IMAGE__|${SAMPLE_IMAGE}|g" \
  "${ROOT_DIR}/test/e2e/manifests/mcp-server.yaml" >"${TEMP_DIR}/mcp-server.yaml"
kubectl --context "${KUBE_CONTEXT}" apply -f "${TEMP_DIR}/mcp-server.yaml"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  rollout status deployment/example-mcp-server --timeout=120s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Registered --timeout=120s

log "starting local API port-forwards"
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
  port-forward service/kavrynt-registry 18081:8080 \
  >"${TEMP_DIR}/registry-port-forward.log" 2>&1 &
PORT_FORWARD_PIDS+=("$!")
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
  port-forward service/kavrynt-gateway 18080:8080 \
  >"${TEMP_DIR}/gateway-port-forward.log" 2>&1 &
PORT_FORWARD_PIDS+=("$!")

wait_for_url "http://127.0.0.1:18081/readyz"
wait_for_url "http://127.0.0.1:18080/readyz"
wait_for_json "http://127.0.0.1:18081/v1/servers" '.servers | length == 1'

SERVER_ID="$(curl --fail --silent http://127.0.0.1:18081/v1/servers | jq --raw-output '.servers[0].id // .servers[0].manifest.metadata.name')"
[ -n "${SERVER_ID}" ] && [ "${SERVER_ID}" != "null" ] || fail "Registry did not return a server identity"
wait_for_json "http://127.0.0.1:18080/v1/routes" ".routes | map(.id // .name) | index(\"${SERVER_ID}\") != null"
KAVRYNT_REGISTRY_URL=http://127.0.0.1:18081 "${TEMP_DIR}/kavryctl" list | grep --fixed-strings "${SERVER_ID}" >/dev/null

log "calling tools/list through Gateway route ${SERVER_ID}"
MCP_RESPONSE="$(curl --fail --silent --show-error \
  --request POST "http://127.0.0.1:18080/mcp/${SERVER_ID}" \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
printf '%s' "${MCP_RESPONSE}" | jq --exit-status '.result.tools[0].name == "echo"' >/dev/null

log "verifying deletion cleanup"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/example-mcp-server --wait=true --timeout=120s
wait_for_json "http://127.0.0.1:18081/v1/servers" '.servers | length == 0'
wait_for_json "http://127.0.0.1:18080/v1/routes" '.routes | length == 0' 30

log "Alpha-0 end-to-end workflow passed"
