#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_VERSION="${RELEASE_VERSION:-}"
RELEASE_VERSION="${RELEASE_VERSION#v}"
[ -n "${RELEASE_VERSION}" ] || {
  printf 'error: RELEASE_VERSION is required (for example, 0.0.2-beta.1)\n' >&2
  exit 1
}

case "${RELEASE_VERSION}" in
  *[!0-9A-Za-z.-]*)
    printf 'error: invalid RELEASE_VERSION: %s\n' "${RELEASE_VERSION}" >&2
    exit 1
    ;;
esac

IMAGE_REGISTRY="${KAVRYNT_IMAGE_REGISTRY:-docker.io/kavrynt}"
# Defaults match the public trial path: images and chart from Docker Hub.
CHART_REF="${KAVRYNT_CHART_REF:-oci://registry-1.docker.io/kavrynt/kavrynt}"
CLUSTER_NAME="${KIND_CLUSTER_NAME:-kavrynt-release-${RELEASE_VERSION//./-}}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
CONTROL_NAMESPACE="kavrynt-system"
E2E_NAMESPACE="kavrynt-e2e"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

GATEWAY_IMAGE="${IMAGE_REGISTRY}/gateway:${RELEASE_VERSION}"
OPERATOR_IMAGE="${IMAGE_REGISTRY}/operator:${RELEASE_VERSION}"
SAMPLE_IMAGE="kavrynt/e2e-mcp-server:${RELEASE_VERSION}-verification"

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

diagnostics() {
  log "collecting failure diagnostics"
  kubectl --context "${KUBE_CONTEXT}" get nodes -o wide || true
  kubectl --context "${KUBE_CONTEXT}" get pods -A -o wide || true
  kubectl --context "${KUBE_CONTEXT}" get events -A --sort-by=.lastTimestamp || true
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

for command in docker kind kubectl helm curl grep jq sed; do
  require_command "${command}"
done

docker info >/dev/null 2>&1 || fail "Docker is not reachable"
if kind get clusters | grep --fixed-strings --line-regexp "${CLUSTER_NAME}" >/dev/null 2>&1; then
  fail "Kind cluster ${CLUSTER_NAME} already exists; delete it or set KIND_CLUSTER_NAME"
fi

log "pulling released runtime images"
docker pull "${GATEWAY_IMAGE}"
docker pull "${OPERATOR_IMAGE}"

log "building verification MCP server"
docker build --tag "${SAMPLE_IMAGE}" "${ROOT_DIR}/test/e2e/mcp-server"

log "creating Kind cluster ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --wait 120s
CREATED_CLUSTER=1

log "loading released images"
kind load docker-image --name "${CLUSTER_NAME}" \
  "${GATEWAY_IMAGE}" "${OPERATOR_IMAGE}" "${SAMPLE_IMAGE}"

log "installing published Kavrynt chart ${RELEASE_VERSION}"
helm upgrade --install kavrynt "${CHART_REF}" \
  --version "${RELEASE_VERSION}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --create-namespace \
  --set gateway.image.repository="${IMAGE_REGISTRY}/gateway" \
  --set-string gateway.image.tag="${RELEASE_VERSION}" \
  --set operator.image.repository="${IMAGE_REGISTRY}/operator" \
  --set-string operator.image.tag="${RELEASE_VERSION}" \
  --wait \
  --timeout 180s

for deployment in kavrynt-gateway kavrynt-operator; do
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
    rollout status "deployment/${deployment}" --timeout=120s
done

log "deploying the verification MCP server"
sed "s|__SAMPLE_IMAGE__|${SAMPLE_IMAGE}|g" \
  "${ROOT_DIR}/test/e2e/manifests/mcp-server.yaml" >"${TEMP_DIR}/mcp-server.yaml"
kubectl --context "${KUBE_CONTEXT}" apply -f "${TEMP_DIR}/mcp-server.yaml"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  rollout status deployment/example-mcp-server --timeout=120s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s

log "starting Gateway port-forward"
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
  port-forward service/kavrynt-gateway 18080:8080 \
  >"${TEMP_DIR}/gateway-port-forward.log" 2>&1 &
PORT_FORWARD_PIDS+=("$!")

wait_for_url "http://127.0.0.1:18080/readyz"
SERVER_ID="${E2E_NAMESPACE}.example-mcp-server"
wait_for_json "http://127.0.0.1:18080/v1/routes" ".routes | map(.name) | index(\"${SERVER_ID}\") != null"

log "calling tools/list through Gateway route ${SERVER_ID}"
MCP_RESPONSE="$(curl --fail --silent --show-error \
  --request POST "http://127.0.0.1:18080/mcp/${SERVER_ID}" \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
printf '%s' "${MCP_RESPONSE}" | jq --exit-status '.result.tools[0].name == "echo"' >/dev/null

log "verifying deletion cleanup"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/example-mcp-server --wait=true --timeout=120s
wait_for_json "http://127.0.0.1:18080/v1/routes" '.routes | length == 0' 30

log "published release ${RELEASE_VERSION} end-to-end workflow passed"
