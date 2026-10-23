#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${KIND_CLUSTER_NAME:-kavrynt-alpha0}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
CONTROL_NAMESPACE="kavrynt-system"
E2E_NAMESPACE="kavrynt-e2e"
LOCAL_VERSION="${LOCAL_VERSION:-0.0.5-beta.1-local}"
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

# start_gateway_port_forward sets GATEWAY_URL. kubectl picks a free local
# port: a fixed one may belong to another local cluster, whose Gateway would
# then answer instead.
start_gateway_port_forward() {
  local log_file="${TEMP_DIR}/gateway-port-forward.log"
  local port=""
  local i
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
    port-forward --address 127.0.0.1 service/kavrynt-gateway :8080 \
    >"${log_file}" 2>&1 &
  PORT_FORWARD_PIDS+=("$!")
  for ((i = 1; i <= 30; i++)); do
    port="$(tr -d '\r' <"${log_file}" |
      sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9][0-9]*\) -> .*/\1/p' | head -n 1)"
    if [ -n "${port}" ]; then
      GATEWAY_URL="http://127.0.0.1:${port}"
      return 0
    fi
    sleep 1
  done
  fail "Gateway port-forward did not start: $(cat "${log_file}")"
}

for command in docker kind kubectl helm curl jq git grep sed; do
  require_command "${command}"
done

docker info >/dev/null 2>&1 || fail "Docker is not reachable"

if kind get clusters | grep --fixed-strings --line-regexp "${CLUSTER_NAME}" >/dev/null 2>&1; then
  fail "Kind cluster ${CLUSTER_NAME} already exists; delete it or set KIND_CLUSTER_NAME"
fi

# Helm installs every chart under charts/, so an old `helm dependency build`
# package would deploy next to the vendored subcharts.
for archive in "${ROOT_DIR}"/charts/kavrynt/charts/*.tgz; do
  [ -e "${archive}" ] || continue
  fail "stale packaged subchart ${archive}; subcharts are vendored as directories"
done

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
# Verifies SPEC-0005-R13: the console admin Secret is mounted, not read.
[ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i get secrets --as="${GATEWAY_SA}" -A)" = "no" ] ||
  fail "Gateway must not read Secrets"

log "verifying the Operator has the permissions it uses"
if kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs deployment/kavrynt-operator |
  grep --fixed-strings --quiet "forbidden"; then
  fail "Operator logged a forbidden API request; check its RBAC"
fi

log "verifying the Operator records discovered tools in MCPServer status"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=ToolsDiscovered --timeout=60s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" get mcpserver/example-mcp-server -o json |
  jq --exit-status '.status.tools | (map(.name) == ["echo"]) and (.[0].inputSchema.required == ["message"])' >/dev/null ||
  fail "MCPServer status does not list the echo tool with its input schema"

# Verifies SPEC-0004-R01 and SPEC-0004-R05.
log "verifying the Operator records the reported server version"
REPORTED_VERSION="$(kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  get mcpserver/example-mcp-server -o jsonpath='{.status.serverInfo.name} {.status.serverInfo.version}')"
[ "${REPORTED_VERSION}" = "kavrynt-alpha0 0.0.1-beta.1" ] ||
  fail "status.serverInfo is '${REPORTED_VERSION}', want the name and version the server reports"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" get mcpserver/example-mcp-server |
  awk 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == "REPORTED") col = i } NR == 2 { print $col }' |
  grep --fixed-strings --quiet "0.0.1-beta.1" ||
  fail "kubectl get mcpservers does not show the reported version"

log "verifying a failed discovery leaves the server Ready"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply -f - >/dev/null <<MANIFEST
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: unreachable-mcp-server
spec:
  version: 0.0.1
  transport: http
  endpoint: http://unreachable-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080
MANIFEST
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/unreachable-mcp-server --for=condition=Ready --timeout=60s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/unreachable-mcp-server \
  --for=jsonpath='{.status.conditions[?(@.type=="ToolsDiscovered")].reason}'=DiscoveryFailed --timeout=60s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/unreachable-mcp-server --wait=true --timeout=60s >/dev/null

log "starting Gateway port-forward"
start_gateway_port_forward
wait_for_url "${GATEWAY_URL}/readyz"

SERVER_ID="${E2E_NAMESPACE}.example-mcp-server"
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SERVER_ID}\") != null"
"${TEMP_DIR}/kavryctl" list --context "${KUBE_CONTEXT}" -A | grep --fixed-strings "/mcp/${SERVER_ID}" >/dev/null ||
  fail "kavryctl list does not show ${SERVER_ID}"

# Verifies SPEC-0005-R02: agents call MCP routes without any console session.
log "calling tools/list through Gateway route ${SERVER_ID}"
MCP_RESPONSE="$(curl --fail --silent --show-error \
  --request POST "${GATEWAY_URL}/mcp/${SERVER_ID}" \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
printf '%s' "${MCP_RESPONSE}" | jq --exit-status '.result.tools[0].name == "echo"' >/dev/null

log "verifying MCP traffic metrics"
call_tool() {
  curl --fail --silent --show-error \
    --request POST "${GATEWAY_URL}/mcp/${SERVER_ID}" \
    --header 'Content-Type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":{\"message\":\"secret-argument\"}}}"
}
call_tool echo | jq --exit-status '.result.content[0].text == "secret-argument"' >/dev/null
call_tool missing_tool | jq --exit-status '.result.isError == true' >/dev/null
METRICS="$(curl --fail --silent --show-error "${GATEWAY_URL}/metrics")"
for series in \
  "kavrynt_gateway_mcp_requests_total{method=\"tools/list\",outcome=\"ok\",route=\"${SERVER_ID}\",tool=\"none\"}" \
  "kavrynt_gateway_mcp_requests_total{method=\"tools/call\",outcome=\"ok\",route=\"${SERVER_ID}\",tool=\"echo\"} 1" \
  "kavrynt_gateway_mcp_requests_total{method=\"tools/call\",outcome=\"tool_error\",route=\"${SERVER_ID}\",tool=\"missing_tool\"} 1"; do
  grep --fixed-strings --quiet "${series}" <<<"${METRICS}" || fail "Gateway metrics missing ${series}"
done
if grep --fixed-strings --quiet "secret-argument" <<<"${METRICS}"; then
  fail "Gateway metrics contain tool arguments"
fi

log "verifying the Gateway does not forward caller credentials"
FORWARDED="$(curl --fail --silent --show-error \
  --header 'Authorization: Bearer caller-token' \
  --header 'Cookie: session=caller' \
  --header 'X-Kavrynt-Probe: kept' \
  "${GATEWAY_URL}/mcp/${SERVER_ID}/debug/headers")"
printf '%s' "${FORWARDED}" | jq --exit-status '
  (has("Authorization") | not) and (has("Cookie") | not) and
  (.["X-Kavrynt-Probe"] == ["kept"])' >/dev/null ||
  fail "Gateway forwarded caller credentials upstream: ${FORWARDED}"

log "verifying the Gateway rejects cross-origin browser requests"
CROSS_ORIGIN_STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}'   --request POST "${GATEWAY_URL}/mcp/${SERVER_ID}"   --header 'Origin: https://attacker.example'   --header 'Content-Type: application/json'   --data '{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{}}')"
[ "${CROSS_ORIGIN_STATUS}" = "403" ] ||
  fail "Gateway answered a cross-origin request with HTTP ${CROSS_ORIGIN_STATUS}, want 403"

# Verifies SPEC-0005-R01, SPEC-0005-R04, and SPEC-0005-R12.
log "verifying the console admin login"
for path in /v1/servers /v1/traffic; do
  STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' "${GATEWAY_URL}${path}")"
  [ "${STATUS}" = "401" ] || fail "${path} without a console session answered HTTP ${STATUS}, want 401"
done
CONSOLE_PASSWORD="$(kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
  get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 --decode)"
[ "${#CONSOLE_PASSWORD}" -eq 32 ] || fail "the generated console admin password is not 32 characters"
CONSOLE_COOKIES="${TEMP_DIR}/console-cookies"
WRONG_STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' --request POST "${GATEWAY_URL}/v1/session" \
  --header 'Content-Type: application/json' --data '{"username":"admin","password":"wrong"}')"
[ "${WRONG_STATUS}" = "401" ] || fail "a wrong console password answered HTTP ${WRONG_STATUS}, want 401"
jq --null-input --arg password "${CONSOLE_PASSWORD}" '{username: "admin", password: $password}' |
  curl --fail --silent --show-error --cookie-jar "${CONSOLE_COOKIES}" --request POST "${GATEWAY_URL}/v1/session" \
    --header 'Content-Type: application/json' --data @- ||
  fail "console login with the generated admin password failed"
curl --fail --silent --show-error --cookie "${CONSOLE_COOKIES}" "${GATEWAY_URL}/v1/session" |
  jq --exit-status '.username == "admin"' >/dev/null || fail "/v1/session does not report the admin session"

# Verifies SPEC-0001-R01, SPEC-0001-R06, SPEC-0001-R07, and SPEC-0001-R08.
log "verifying the console and its read-only endpoints"
curl --fail --silent --show-error "${GATEWAY_URL}/ui/" | grep --fixed-strings --quiet "Kavrynt Console" ||
  fail "Gateway does not serve the console at /ui/"
curl --fail --silent --show-error --cookie "${CONSOLE_COOKIES}" "${GATEWAY_URL}/v1/servers" |
  jq --exit-status --arg route "${SERVER_ID}" '
    .servers | map(select(.route == $route)) | .[0] |
    .routed and (.tools | map(.name) == ["echo"])' >/dev/null ||
  fail "/v1/servers does not list ${SERVER_ID} as routed with the echo tool"
TRAFFIC="$(curl --fail --silent --show-error --cookie "${CONSOLE_COOKIES}" "${GATEWAY_URL}/v1/traffic")"
printf '%s' "${TRAFFIC}" | jq --exit-status --arg route "${SERVER_ID}" '
  .requests | any(.route == $route and .method == "tools/call" and .tool == "echo" and .outcome == "ok")' >/dev/null ||
  fail "/v1/traffic does not record the echo tool call"
if grep --fixed-strings --quiet "secret-argument" <<<"${TRAFFIC}"; then
  fail "/v1/traffic contains tool arguments"
fi

# The console in a real browser: login, keyboard, layout, and traffic views.
# test/console-smoke/smoke.mjs cites the requirements it verifies. CI sets
# E2E_BROWSER=1; locally it runs when its dependencies are installed.
SMOKE_DIR="${ROOT_DIR}/test/console-smoke"
if [ "${E2E_BROWSER:-auto}" = "0" ]; then
  log "skipping the console browser smoke test (E2E_BROWSER=0)"
elif [ -d "${SMOKE_DIR}/node_modules/playwright" ]; then
  log "verifying the console in a headless browser"
  GATEWAY_URL="${GATEWAY_URL}" CONSOLE_PASSWORD="${CONSOLE_PASSWORD}" SERVER_ROUTE="${SERVER_ID}" SERVER_TOOL=echo \
    node "${SMOKE_DIR}/smoke.mjs" || fail "console browser smoke test failed"
elif [ "${E2E_BROWSER:-auto}" = "1" ]; then
  fail "E2E_BROWSER=1 but ${SMOKE_DIR} has no dependencies; run: npm ci --prefix test/console-smoke && npm exec --prefix test/console-smoke -- playwright install chromium-headless-shell"
else
  log "skipping the console browser smoke test: run npm ci --prefix test/console-smoke && npm exec --prefix test/console-smoke -- playwright install chromium-headless-shell"
fi

log "verifying the Gateway refuses cloud metadata endpoints"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply -f - >/dev/null <<MANIFEST
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: metadata-endpoint
spec:
  version: 0.0.1
  transport: http
  endpoint: http://169.254.169.254/latest/meta-data/
MANIFEST
METADATA_ID="${E2E_NAMESPACE}.metadata-endpoint"
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${METADATA_ID}\") != null"
METADATA_STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 10   "${GATEWAY_URL}/mcp/${METADATA_ID}")"
[ "${METADATA_STATUS}" = "502" ] ||
  fail "Gateway answered a request to a metadata endpoint with HTTP ${METADATA_STATUS}, want 502"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}"   wait mcpserver/metadata-endpoint   --for=jsonpath='{.status.conditions[?(@.type=="ToolsDiscovered")].reason}'=DiscoveryFailed --timeout=60s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}"   delete mcpserver/metadata-endpoint --wait=true --timeout=60s >/dev/null
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${METADATA_ID}\") == null" 30

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
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SECOND_ID}\") != null"
"${TEMP_DIR}/kavryctl" inspect --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" kavryctl-mcp-server |
  jq --exit-status '.status.conditions | map(select(.type == "Ready")) | .[0].status == "True"' >/dev/null
curl --fail --silent --show-error \
  --request POST "${GATEWAY_URL}/mcp/${SECOND_ID}" \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' |
  jq --exit-status '.result.tools[0].name == "echo"' >/dev/null
"${TEMP_DIR}/kavryctl" unregister --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" kavryctl-mcp-server >/dev/null
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SECOND_ID}\") == null" 30

log "verifying deletion cleanup"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/example-mcp-server --wait=true --timeout=120s
wait_for_json "${GATEWAY_URL}/v1/routes" '.routes | length == 0' 30

log "end-to-end workflow passed"
