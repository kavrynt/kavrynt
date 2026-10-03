#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${KIND_CLUSTER_NAME:-kavrynt-alpha0}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
CONTROL_NAMESPACE="kavrynt-system"
E2E_NAMESPACE="kavrynt-e2e"
LOCAL_VERSION="${LOCAL_VERSION:-0.0.2-beta.1-local}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

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
  git -C "${ROOT_DIR}" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown'
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

for command in docker kind kubectl helm curl jq git grep sed; do
  require_command "${command}"
done

docker info >/dev/null 2>&1 || fail "Docker is not reachable"

if kind get clusters | grep --fixed-strings --line-regexp "${CLUSTER_NAME}" >/dev/null 2>&1; then
  fail "Kind cluster ${CLUSTER_NAME} already exists; delete it or set KIND_CLUSTER_NAME"
fi

BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

log "building kavryctl client"
(
  cd "${ROOT_DIR}"
  GOCACHE="${TEMP_DIR}/go-build" go build -o "${TEMP_DIR}/kavryctl" ./cmd/kavryctl
)

log "building runtime images"
COMMIT="$(git_commit)"
for target in gateway operator; do
  docker build \
    --file "${ROOT_DIR}/build/Dockerfile" \
    --target "${target}" \
    --build-arg VERSION="${LOCAL_VERSION}" \
    --build-arg COMMIT="${COMMIT}" \
    --build-arg BUILD_DATE="${BUILD_DATE}" \
    --tag "kavrynt/${target}:${LOCAL_VERSION}" \
    "${ROOT_DIR}"
done
docker build --tag "${SAMPLE_IMAGE}" "${ROOT_DIR}/test/e2e/mcp-server"

log "creating Kind cluster ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --wait 120s
CREATED_CLUSTER=1

log "loading local images"
kind load docker-image --name "${CLUSTER_NAME}" \
  "${GATEWAY_IMAGE}" "${OPERATOR_IMAGE}" "${SAMPLE_IMAGE}"

log "installing Kavrynt control plane"
helm upgrade --install kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --create-namespace \
  --set gateway.image.repository=kavrynt/gateway \
  --set-string gateway.image.tag="${LOCAL_VERSION}" \
  --set operator.image.repository=kavrynt/operator \
  --set-string operator.image.tag="${LOCAL_VERSION}" \
  --wait \
  --timeout 180s

for deployment in kavrynt-gateway kavrynt-operator; do
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
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s

log "verifying CRD admission rejects unsafe endpoints"
if kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply --dry-run=server -f - >/dev/null 2>&1 <<MANIFEST
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: unsafe-endpoint
spec:
  version: 0.0.1
  transport: http
  endpoint: http://user:secret@example-mcp-server:8080
MANIFEST
then
  fail "API server accepted an endpoint with embedded credentials"
fi

log "verifying Gateway RBAC is read-only"
GATEWAY_SA="system:serviceaccount:${CONTROL_NAMESPACE}:kavrynt-gateway"
for verb in get list watch; do
  [ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i "${verb}" mcpservers.kavrynt.io --as="${GATEWAY_SA}" -A)" = "yes" ] ||
    fail "Gateway cannot ${verb} MCPServers"
done
for verb in create update patch delete; do
  [ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i "${verb}" mcpservers.kavrynt.io --as="${GATEWAY_SA}" -A)" = "no" ] ||
    fail "Gateway must not ${verb} MCPServers"
done
[ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i get secrets --as="${GATEWAY_SA}" -A)" = "no" ] ||
  fail "Gateway must not read Secrets"

log "starting Gateway port-forward"
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
  port-forward service/kavrynt-gateway 18080:8080 \
  >"${TEMP_DIR}/gateway-port-forward.log" 2>&1 &
PORT_FORWARD_PIDS+=("$!")
wait_for_url "http://127.0.0.1:18080/readyz"

SERVER_ID="${E2E_NAMESPACE}.example-mcp-server"
wait_for_json "http://127.0.0.1:18080/v1/routes" ".routes | map(.name) | index(\"${SERVER_ID}\") != null"
"${TEMP_DIR}/kavryctl" list --context "${KUBE_CONTEXT}" -A | grep --fixed-strings "/mcp/${SERVER_ID}" >/dev/null ||
  fail "kavryctl list does not show ${SERVER_ID}"

log "calling tools/list through Gateway route ${SERVER_ID}"
MCP_RESPONSE="$(curl --fail --silent --show-error \
  --request POST "http://127.0.0.1:18080/mcp/${SERVER_ID}" \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
printf '%s' "${MCP_RESPONSE}" | jq --exit-status '.result.tools[0].name == "echo"' >/dev/null

log "verifying the Gateway does not forward caller credentials"
FORWARDED="$(curl --fail --silent --show-error \
  --header 'Authorization: Bearer caller-token' \
  --header 'Cookie: session=caller' \
  --header 'X-Kavrynt-Probe: kept' \
  "http://127.0.0.1:18080/mcp/${SERVER_ID}/debug/headers")"
printf '%s' "${FORWARDED}" | jq --exit-status '
  (has("Authorization") | not) and (has("Cookie") | not) and
  (.["X-Kavrynt-Probe"] == ["kept"])' >/dev/null ||
  fail "Gateway forwarded caller credentials upstream: ${FORWARDED}"

log "registering a second route with kavryctl"
cat >"${TEMP_DIR}/kavryctl-server.yaml" <<MANIFEST
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: kavryctl-mcp-server
spec:
  version: 0.0.1
  transport: http
  endpoint: http://example-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080
MANIFEST
"${TEMP_DIR}/kavryctl" validate "${TEMP_DIR}/kavryctl-server.yaml" >/dev/null
"${TEMP_DIR}/kavryctl" register --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" "${TEMP_DIR}/kavryctl-server.yaml" >/dev/null
SECOND_ID="${E2E_NAMESPACE}.kavryctl-mcp-server"
wait_for_json "http://127.0.0.1:18080/v1/routes" ".routes | map(.name) | index(\"${SECOND_ID}\") != null"
"${TEMP_DIR}/kavryctl" inspect --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" kavryctl-mcp-server |
  jq --exit-status '.status.conditions | map(select(.type == "Ready")) | .[0].status == "True"' >/dev/null
curl --fail --silent --show-error \
  --request POST "http://127.0.0.1:18080/mcp/${SECOND_ID}" \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' |
  jq --exit-status '.result.tools[0].name == "echo"' >/dev/null
"${TEMP_DIR}/kavryctl" unregister --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" kavryctl-mcp-server >/dev/null
wait_for_json "http://127.0.0.1:18080/v1/routes" ".routes | map(.name) | index(\"${SECOND_ID}\") == null" 30

log "verifying deletion cleanup"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/example-mcp-server --wait=true --timeout=120s
wait_for_json "http://127.0.0.1:18080/v1/routes" '.routes | length == 0' 30

log "end-to-end workflow passed"
