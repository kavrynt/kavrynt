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

# start_port_forward sets variable $4 to a local URL for port $3 of Service
# $2 in namespace $1. It runs in this shell (not in $(...)) so cleanup can
# stop the forward.
start_port_forward() {
  local namespace=$1 service=$2 target=$3 variable=$4 log_file port="" i
  log_file="${TEMP_DIR}/${service}-port-forward.log"
  kubectl --context "${KUBE_CONTEXT}" -n "${namespace}" \
    port-forward --address 127.0.0.1 "service/${service}" ":${target}" >"${log_file}" 2>&1 &
  PORT_FORWARD_PIDS+=("$!")
  for ((i = 1; i <= 30; i++)); do
    port="$(tr -d '\r' <"${log_file}" | sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9][0-9]*\) -> .*/\1/p' | head -n 1)"
    if [ -n "${port}" ]; then
      printf -v "${variable}" 'http://127.0.0.1:%s' "${port}"
      return 0
    fi
    sleep 1
  done
  fail "port-forward to ${service} did not start: $(cat "${log_file}")"
}

stop_gateway_port_forwards() {
  if [ "${#PORT_FORWARD_PIDS[@]}" -gt 0 ]; then
    kill "${PORT_FORWARD_PIDS[@]}" >/dev/null 2>&1 || true
    wait "${PORT_FORWARD_PIDS[@]}" >/dev/null 2>&1 || true
  fi
  PORT_FORWARD_PIDS=()
}

# verify_installed_crd checks that the CRD in the cluster is the chart's,
# owned by Helm and kept on uninstall, with the versions of
# config/crd/bases (the API server adds defaults only outside .spec.versions).
verify_installed_crd() {
  local installed expected
  installed="$(kubectl --context "${KUBE_CONTEXT}" get crd mcpservers.kavrynt.io -o json)"
  printf '%s' "${installed}" | jq --exit-status '
    .metadata.labels["app.kubernetes.io/managed-by"] == "Helm" and
    .metadata.annotations["helm.sh/resource-policy"] == "keep"' >/dev/null ||
    fail "the MCPServer CRD is not managed by Helm with the keep policy"
  expected="$(kubectl --context "${KUBE_CONTEXT}" create --dry-run=client -o json \
    -f "${ROOT_DIR}/config/crd/bases/kavrynt.io_mcpservers.yaml" | jq --sort-keys '.spec.versions')"
  [ "$(printf '%s' "${installed}" | jq --sort-keys '.spec.versions')" = "${expected}" ] ||
    fail "the installed MCPServer CRD differs from config/crd/bases"
}

call_tools_list() {
  curl --fail --silent --show-error \
    --request POST "${GATEWAY_URL}/mcp/$1" \
    --header 'Content-Type: application/json' \
    --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' |
    jq --exit-status '.result.tools[0].name == "echo"' >/dev/null
}

# probe runs the e2e MCP server image's load client once from a pod in
# namespace $1 against URL $2 for about a second and prints its summary line
# ("done ok=N failed=M"). A call blocked by a NetworkPolicy times out after
# five seconds and counts as failed.
probe() {
  local namespace=$1 url=$2 name="probe-$RANDOM"
  kubectl --context "${KUBE_CONTEXT}" -n "${namespace}" apply -f - >/dev/null <<MANIFEST
apiVersion: v1
kind: Pod
metadata:
  name: ${name}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: ${SAMPLE_IMAGE}
      imagePullPolicy: Never
      args: [load, -url, "${url}", -duration, 1s, -timeout, 5s]
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        readOnlyRootFilesystem: true
MANIFEST
  kubectl --context "${KUBE_CONTEXT}" -n "${namespace}" wait "pod/${name}" \
    --for=jsonpath='{.status.phase}'=Succeeded --timeout=90s >/dev/null
  kubectl --context "${KUBE_CONTEXT}" -n "${namespace}" logs "pod/${name}" | grep '^done'
  kubectl --context "${KUBE_CONTEXT}" -n "${namespace}" delete "pod/${name}" --wait=false >/dev/null
}

deployment_generations() {
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" get deployment kavrynt-gateway kavrynt-operator \
    -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.generation} {end}'
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

# Verifies SPEC-0008-R10: the end-to-end test runs the production profile.
# The NetworkPolicies (SPEC-0007) are on from the start, so every later check
# runs with MCP servers reachable only through the Gateway and the Operator.
# The policy for the MCP server namespace needs that namespace to exist.
kubectl --context "${KUBE_CONTEXT}" create namespace "${E2E_NAMESPACE}" >/dev/null
# The upstream credentials Secret (SPEC-0011) exists before the pods start,
# so the test does not wait for the kubelet to refresh a mount.
UPSTREAM_SECRET_VALUE="e2e-upstream-secret-$(date +%s)"
kubectl --context "${KUBE_CONTEXT}" create namespace "${CONTROL_NAMESPACE}" >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" create secret generic kavrynt-upstream-credentials \
  --from-literal="${E2E_NAMESPACE}.secured-mcp-server=${UPSTREAM_SECRET_VALUE}" \
  --from-literal="${E2E_NAMESPACE}.secured-mcp-server.origin=http://secured-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080" >/dev/null
# Verifies SPEC-0014-R01, SPEC-0014-R02, and SPEC-0014-R06: an install with
# default values names both ways forward and creates no release.
AUTH_REFUSAL="MCP client authentication is on by default"
log "verifying an install with default values is refused"
if helm install kavrynt "${ROOT_DIR}/charts/kavrynt" --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" >"${TEMP_DIR}/default-install.log" 2>&1; then
  fail "helm install with default values succeeded without MCP client authentication settings"
fi
for want in "${AUTH_REFUSAL}" gateway.auth.issuers gateway.auth.enabled=false; do
  grep --fixed-strings --quiet "${want}" "${TEMP_DIR}/default-install.log" ||
    fail "the refused install does not name ${want}: $(cat "${TEMP_DIR}/default-install.log")"
done
if helm status kavrynt --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" >/dev/null 2>&1; then
  fail "the refused install left a release behind"
fi

# The first install opts out of MCP client authentication explicitly
# (SPEC-0014-R06): the checks before the authentication section call routes
# without tokens. Authentication is turned on later by upgrade.
log "installing Kavrynt control plane with the production profile"
helm upgrade --install kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --create-namespace \
  --values "${ROOT_DIR}/examples/helm/values-production.yaml" \
  --set gateway.auth.enabled=false \
  --set global.upstreamCredentials.secretName=kavrynt-upstream-credentials \
  --set networkPolicy.mcpServers.enabled=true \
  --set "networkPolicy.mcpServers.namespaces={${E2E_NAMESPACE}}" \
  --set networkPolicy.gateway.enabled=true \
  --set-json "networkPolicy.gateway.allowedNamespaceLabels=[{\"kubernetes.io/metadata.name\":\"${E2E_NAMESPACE}\"}]" \
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

# Verifies SPEC-0014-R03. The logs are read first: grep --quiet would stop
# reading early and fail the pipeline.
GATEWAY_LOGS="$(kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs --tail=-1 \
  --selector app.kubernetes.io/name=gateway,app.kubernetes.io/instance=kavrynt)"
grep --fixed-strings --quiet "MCP client authentication is off" <<<"${GATEWAY_LOGS}" ||
  fail "the Gateway did not warn that MCP routes are unauthenticated"

log "deploying the Alpha-0 MCP server"
sed "s|__SAMPLE_IMAGE__|${SAMPLE_IMAGE}|g" \
  "${ROOT_DIR}/test/e2e/manifests/mcp-server.yaml" >"${TEMP_DIR}/mcp-server.yaml"
kubectl --context "${KUBE_CONTEXT}" apply -f "${TEMP_DIR}/mcp-server.yaml"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  rollout status deployment/example-mcp-server --timeout=120s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s

# Verifies SPEC-0009-R01 and SPEC-0009-R04.
log "verifying the chart installed the CRD from config/crd/bases"
verify_installed_crd

log "verifying CRD admission rejects unsafe endpoints and credential headers"
# Verifies SPEC-0011-R01.
if kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply --dry-run=server -f - >/dev/null 2>&1 <<MANIFEST
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: cookie-credential
spec:
  version: 0.0.1
  transport: http
  endpoint: http://example-mcp-server:8080
  credential:
    header: Cookie
MANIFEST
then
  fail "API server accepted a credential sent as a Cookie"
fi
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
if kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs --tail=-1 \
  --selector app.kubernetes.io/name=operator,app.kubernetes.io/instance=kavrynt |
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
# Verifies SPEC-0008-R06: recent traffic names the replica that answered.
TRAFFIC_INSTANCE="$(printf '%s' "${TRAFFIC}" | jq -r '.instance')"
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" get pod "${TRAFFIC_INSTANCE}" \
  --output jsonpath='{.metadata.labels.app\.kubernetes\.io/name}' 2>/dev/null | grep --fixed-strings --quiet gateway ||
  fail "/v1/traffic instance '${TRAFFIC_INSTANCE}' is not a Gateway pod"

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

# Verifies SPEC-0011-R01, SPEC-0011-R02, SPEC-0011-R10: an MCP server that
# requires its own bearer token is routed with the stored credential, a
# server whose credential is not stored is refused, and neither component
# can read Secrets.
log "verifying upstream credentials from the mounted Secret"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply -f - >/dev/null <<MANIFEST
apiVersion: apps/v1
kind: Deployment
metadata:
  name: secured-mcp-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: secured-mcp-server
  template:
    metadata:
      labels:
        app.kubernetes.io/name: secured-mcp-server
        kavrynt.io/gateway-only: "true"
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: mcp-server
          image: ${SAMPLE_IMAGE}
          imagePullPolicy: Never
          env:
            - name: REQUIRE_BEARER
              value: ${UPSTREAM_SECRET_VALUE}
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: [ALL]
            readOnlyRootFilesystem: true
---
apiVersion: v1
kind: Service
metadata:
  name: secured-mcp-server
spec:
  selector:
    app.kubernetes.io/name: secured-mcp-server
  ports:
    - name: http
      port: 8080
      targetPort: http
---
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: secured-mcp-server
spec:
  version: 0.0.1
  transport: http
  endpoint: http://secured-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080
  credential:
    scheme: Bearer
---
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: unstored-credential
spec:
  version: 0.0.1
  transport: http
  endpoint: http://secured-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080
  credential:
    scheme: Bearer
MANIFEST
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" rollout status deployment/secured-mcp-server --timeout=120s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/secured-mcp-server --for=condition=ToolsDiscovered --timeout=120s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" get mcpserver/secured-mcp-server -o json |
  jq --exit-status '.status.tools | map(.name) == ["echo"]' >/dev/null ||
  fail "the Operator did not discover the tools of a server that needs its stored credential"
SECURED_ID="${E2E_NAMESPACE}.secured-mcp-server"
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SECURED_ID}\") != null"
curl --fail --silent --show-error --request POST "${GATEWAY_URL}/mcp/${SECURED_ID}" \
  --header 'Content-Type: application/json' --header 'Authorization: Bearer caller-token' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' |
  jq --exit-status '.result.tools[0].name == "echo"' >/dev/null ||
  fail "the Gateway did not send the stored credential to the secured MCP server"

kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" wait mcpserver/unstored-credential --for=condition=Ready --timeout=60s
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" wait mcpserver/unstored-credential \
  --for=jsonpath='{.status.conditions[?(@.type=="ToolsDiscovered")].reason}'=CredentialUnavailable --timeout=60s
UNSTORED_ID="${E2E_NAMESPACE}.unstored-credential"
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${UNSTORED_ID}\") != null"
STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' --request POST "${GATEWAY_URL}/mcp/${UNSTORED_ID}" \
  --header 'Content-Type: application/json' --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
[ "${STATUS}" = "503" ] || fail "a server without a stored credential answered HTTP ${STATUS}, want 503"

OPERATOR_SA="system:serviceaccount:${CONTROL_NAMESPACE}:kavrynt-operator"
[ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i get secrets --as="${OPERATOR_SA}" -A)" = "no" ] ||
  fail "the Operator must not read Secrets"
[ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i get secrets --as="${GATEWAY_SA}" -A)" = "no" ] ||
  fail "the Gateway must not read Secrets"
# Verifies SPEC-0011-R09.
SERVERS_JSON="$(curl --fail --silent --show-error --cookie "${CONSOLE_COOKIES}" "${GATEWAY_URL}/v1/servers")"
printf '%s' "${SERVERS_JSON}" | jq --exit-status --arg route "${SECURED_ID}" \
  '.servers | map(select(.route == $route)) | .[0].credential == {"required": true, "available": true}' >/dev/null ||
  fail "/v1/servers does not report the stored credential as available"
for output in "${SERVERS_JSON}" "$(curl --fail --silent --show-error "${GATEWAY_URL}/metrics")" \
  "$(kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" get mcpservers -o yaml)" \
  "$(kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs --tail=-1 --selector app.kubernetes.io/instance=kavrynt)"; do
  if grep --fixed-strings --quiet "${UPSTREAM_SECRET_VALUE}" <<<"${output}"; then
    fail "the upstream credential appears in an API response, status, or log"
  fi
done
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" delete mcpserver/secured-mcp-server mcpserver/unstored-credential --wait=true --timeout=60s >/dev/null

# Verifies SPEC-0007-R09. Tool discovery and every Gateway call above already
# ran under the policies; here a direct call to the labelled MCP server, even
# from its own namespace, must fail, while the same pod reaches it through
# the Gateway. A namespace the Gateway policy does not allow cannot call it.
log "verifying MCP servers are reachable only through the Gateway"
MCP_SERVICE_URL="http://example-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080/"
GATEWAY_SERVICE_URL="http://kavrynt-gateway.${CONTROL_NAMESPACE}.svc.cluster.local:8080/mcp/${SERVER_ID}"
DIRECT="$(probe "${E2E_NAMESPACE}" "${MCP_SERVICE_URL}")"
grep --quiet --extended-regexp '^done ok=0 failed=[1-9]' <<<"${DIRECT}" || fail "a pod called the MCP server directly despite the NetworkPolicy: ${DIRECT}"
VIA_GATEWAY="$(probe "${E2E_NAMESPACE}" "${GATEWAY_SERVICE_URL}")"
grep --quiet --extended-regexp '^done ok=[1-9][0-9]* failed=0$' <<<"${VIA_GATEWAY}" || fail "a pod in an allowed namespace could not call through the Gateway: ${VIA_GATEWAY}"
FROM_DEFAULT="$(probe default "${GATEWAY_SERVICE_URL}")"
grep --quiet --extended-regexp '^done ok=0 failed=[1-9]' <<<"${FROM_DEFAULT}" || fail "a pod in a namespace the Gateway policy does not allow called the Gateway: ${FROM_DEFAULT}"

# Verifies SPEC-0008-R01, SPEC-0008-R03, and SPEC-0008-R11: a client inside
# the cluster calls the Gateway Service throughout a rolling restart of every
# replica, and no call fails.
log "verifying a Gateway rolling restart fails no MCP call"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply -f - >/dev/null <<MANIFEST
apiVersion: v1
kind: Pod
metadata:
  name: gateway-load
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: load
      image: ${SAMPLE_IMAGE}
      imagePullPolicy: Never
      args:
        - load
        - -url
        - http://kavrynt-gateway.${CONTROL_NAMESPACE}.svc.cluster.local:8080/mcp/${SERVER_ID}
        - -duration
        - 10m
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        readOnlyRootFilesystem: true
MANIFEST
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" wait pod/gateway-load --for=condition=Ready --timeout=60s
# Let the client reach steady state with pooled connections first.
for ((i = 1; i <= 30; i++)); do
  kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" logs pod/gateway-load | grep --quiet '^progress ok=[1-9]' && break
  sleep 1
done
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" rollout restart deployment/kavrynt-gateway >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" rollout status deployment/kavrynt-gateway --timeout=300s
# The last replaced pod drains for its shutdown delay after rollout status returns.
sleep 10
LOAD_LOG="$(kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" logs pod/gateway-load)"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" delete pod/gateway-load --wait=false >/dev/null
if grep --quiet '^FAIL' <<<"${LOAD_LOG}"; then
  fail "MCP calls failed during the Gateway rolling restart: $(grep '^FAIL' <<<"${LOAD_LOG}" | head -n 5)"
fi
grep --quiet '^progress ok=[1-9]' <<<"${LOAD_LOG}" || fail "the load client made no successful call: ${LOAD_LOG}"
log "rolling restart: $(grep '^progress' <<<"${LOAD_LOG}" | tail -n 1)"
stop_gateway_port_forwards
start_gateway_port_forward
wait_for_url "${GATEWAY_URL}/readyz"

# Verifies SPEC-0008-R08 and SPEC-0008-R11: deleting the Operator leader lets
# the standby replica reconcile new servers.
log "verifying a standby Operator takes over from a deleted leader"
LEADER="$(kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" get lease kavrynt-k8s-operator \
  -o jsonpath='{.spec.holderIdentity}')"
LEADER_POD="${LEADER%%_*}"
[ -n "${LEADER_POD}" ] || fail "no Operator holds the leader lease"
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" delete pod "${LEADER_POD}" --wait=false >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply -f - >/dev/null <<MANIFEST
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: failover-mcp-server
spec:
  version: 0.0.1
  transport: http
  endpoint: http://example-mcp-server.${E2E_NAMESPACE}.svc.cluster.local:8080
MANIFEST
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/failover-mcp-server --for=condition=Ready --timeout=120s
NEW_LEADER="$(kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" get lease kavrynt-k8s-operator \
  -o jsonpath='{.spec.holderIdentity}')"
[ "${NEW_LEADER%%_*}" != "${LEADER_POD}" ] || fail "the deleted Operator ${LEADER_POD} still holds the lease"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/failover-mcp-server --wait=true --timeout=60s >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
  rollout status deployment/kavrynt-operator --timeout=120s

# MCP client authentication (SPEC-0006) with a test authorization server, the
# e2e image's issuer mode, reached by the Gateway over in-cluster http.
log "deploying a test authorization server"
ISSUER_URL="http://e2e-issuer.${E2E_NAMESPACE}.svc.cluster.local:8080"
RESOURCE_URL="https://mcp.e2e.test"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" apply -f - >/dev/null <<MANIFEST
apiVersion: apps/v1
kind: Deployment
metadata:
  name: e2e-issuer
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: e2e-issuer
  template:
    metadata:
      labels:
        app.kubernetes.io/name: e2e-issuer
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: issuer
          image: ${SAMPLE_IMAGE}
          imagePullPolicy: Never
          args: [issuer]
          env:
            - name: ISSUER
              value: ${ISSUER_URL}
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: [ALL]
            readOnlyRootFilesystem: true
---
apiVersion: v1
kind: Service
metadata:
  name: e2e-issuer
spec:
  selector:
    app.kubernetes.io/name: e2e-issuer
  ports:
    - name: http
      port: 8080
      targetPort: http
MANIFEST
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" rollout status deployment/e2e-issuer --timeout=120s

log "enabling MCP client authentication"
helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --reuse-values \
  --set gateway.auth.enabled=true \
  --set gateway.auth.resourceURL="${RESOURCE_URL}" \
  --set-json "gateway.auth.issuers=[{\"issuer\":\"${ISSUER_URL}\",\"allowHTTP\":true}]" \
  --set "gateway.auth.requiredScopes={mcp:tools}" \
  --wait --timeout 180s >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" rollout status deployment/kavrynt-gateway --timeout=180s
stop_gateway_port_forwards
start_gateway_port_forward
wait_for_url "${GATEWAY_URL}/readyz"
start_port_forward "${E2E_NAMESPACE}" e2e-issuer 8080 ISSUER_LOCAL
wait_for_url "${ISSUER_LOCAL}/healthz"
mint() {
  curl --fail --silent --show-error --get "${ISSUER_LOCAL}/mint" \
    --data-urlencode "aud=$1" --data-urlencode "scope=$2" \
    --data-urlencode "sub=e2e-user" --data-urlencode "client_id=${3:-e2e-agent}"
}
mcp_status() {
  curl --silent --output "${TEMP_DIR}/mcp-body" --dump-header "${TEMP_DIR}/mcp-headers" --write-out '%{http_code}' \
    --request POST "${GATEWAY_URL}/mcp/${SERVER_ID}" --header 'Content-Type: application/json' "$@" \
    --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
}
ROUTE_RESOURCE="${RESOURCE_URL}/mcp/${SERVER_ID}"

# Verifies SPEC-0006-R03 and SPEC-0006-R07.
log "verifying calls without a token are challenged with the route's metadata"
STATUS="$(mcp_status)"
[ "${STATUS}" = "401" ] || fail "a call without a token answered HTTP ${STATUS}, want 401"
grep -i '^www-authenticate:' "${TEMP_DIR}/mcp-headers" |
  grep --fixed-strings --quiet "resource_metadata=\"${RESOURCE_URL}/.well-known/oauth-protected-resource/mcp/${SERVER_ID}\"" ||
  fail "the 401 does not point at the route's protected resource metadata: $(cat "${TEMP_DIR}/mcp-headers")"
curl --fail --silent --show-error "${GATEWAY_URL}/.well-known/oauth-protected-resource/mcp/${SERVER_ID}" |
  jq --exit-status --arg resource "${ROUTE_RESOURCE}" --arg issuer "${ISSUER_URL}" '
    .resource == $resource and .authorization_servers == [$issuer] and
    .bearer_methods_supported == ["header"] and .scopes_supported == ["mcp:tools"]' >/dev/null ||
  fail "the protected resource metadata does not name the route and the issuer"

# Verifies SPEC-0006-R08 and SPEC-0006-R13.
log "verifying a valid token is accepted and never reaches the MCP server"
TOKEN="$(mint "${ROUTE_RESOURCE}" "mcp:tools")"
STATUS="$(mcp_status --header "Authorization: Bearer ${TOKEN}")"
[ "${STATUS}" = "200" ] || fail "a valid token answered HTTP ${STATUS}: $(cat "${TEMP_DIR}/mcp-body")"
jq --exit-status '.result.tools[0].name == "echo"' "${TEMP_DIR}/mcp-body" >/dev/null || fail "tools/list with a valid token returned no tools"
curl --fail --silent --show-error --header "Authorization: Bearer ${TOKEN}" "${GATEWAY_URL}/mcp/${SERVER_ID}/debug/headers" |
  jq --exit-status 'has("Authorization") | not' >/dev/null || fail "the Gateway forwarded the access token to the MCP server"

# Verifies SPEC-0006-R04, SPEC-0006-R06, and SPEC-0006-R09.
log "verifying wrong audiences, missing scopes, and query tokens are refused"
STATUS="$(mcp_status --header "Authorization: Bearer $(mint "${RESOURCE_URL}/mcp/${E2E_NAMESPACE}.other" "mcp:tools")")"
[ "${STATUS}" = "401" ] || fail "a token for another route answered HTTP ${STATUS}, want 401"
STATUS="$(mcp_status --header "Authorization: Bearer $(mint "${ROUTE_RESOURCE}" "openid")")"
[ "${STATUS}" = "403" ] || fail "a token without the required scope answered HTTP ${STATUS}, want 403"
STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' --request POST \
  "${GATEWAY_URL}/mcp/${SERVER_ID}?access_token=${TOKEN}" --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
[ "${STATUS}" = "400" ] || fail "a token in the query string answered HTTP ${STATUS}, want 400"

# Verifies SPEC-0006-R14, SPEC-0006-R15, and SPEC-0006-R19.
log "verifying refusals are recorded and the console and route list know about authentication"
METRICS="$(curl --fail --silent --show-error "${GATEWAY_URL}/metrics")"
grep --quiet "outcome=\"unauthenticated\",route=\"${SERVER_ID}\"" <<<"${METRICS}" ||
  fail "the Gateway did not record unauthenticated calls"
grep --fixed-strings --quiet "${TOKEN}" <<<"${METRICS}" && fail "the Gateway metrics contain an access token"
curl --silent "${GATEWAY_URL}/v1/session" | jq --exit-status '.mcpAuth == true' >/dev/null ||
  fail "/v1/session does not report MCP client authentication"
curl --fail --silent --show-error "${GATEWAY_URL}/v1/routes" |
  jq --exit-status --arg route "${SERVER_ID}" '(.routes | map(.name) | index($route) != null) and all(.routes[]; has("endpoint") | not)' >/dev/null ||
  fail "/v1/routes lists endpoints while MCP client authentication is on"
if kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs --tail=-1 \
  --selector app.kubernetes.io/name=gateway,app.kubernetes.io/instance=kavrynt | grep --fixed-strings --quiet "${TOKEN}"; then
  fail "the Gateway logged an access token"
fi

# Verifies SPEC-0012-R12: a per-client tool allow-list from a ConfigMap
# decides calls of authenticated clients, and a broken update leaves the
# decisions in force.
log "enabling the tool authorization policy"
write_policy() {
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" create configmap kavrynt-policy \
    --from-file=authz.rego=/dev/stdin --dry-run=client -o yaml |
    kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" apply -f - >/dev/null
}
write_policy <<'REGO'
package kavrynt.authz

default decision := {"allow": false, "reason": "tool not allowed for client"}

decision := {"allow": true} if {
	input.issuer != ""
	input.mcp_method != "tools/call"
}

decision := {"allow": true} if {
	input.client_id == "e2e-agent"
	input.tool == "echo"
}
REGO
helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --reuse-values \
  --set gateway.policy.enabled=true \
  --set gateway.policy.configMap=kavrynt-policy \
  --set gateway.policy.filterToolsList=true \
  --wait --timeout 180s >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" rollout status deployment/kavrynt-gateway --timeout=180s
stop_gateway_port_forwards
start_gateway_port_forward
start_port_forward "${E2E_NAMESPACE}" e2e-issuer 8080 ISSUER_LOCAL
wait_for_url "${GATEWAY_URL}/readyz"
echo_call() {
  curl --silent --output "${TEMP_DIR}/policy-body" --write-out '%{http_code}' --request POST "${GATEWAY_URL}/mcp/${SERVER_ID}" \
    --header 'Content-Type: application/json' --header "Authorization: Bearer $1" \
    --data '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{"message":"audit-secret-argument"}}}'
}
ALLOWED_TOKEN="$(mint "${ROUTE_RESOURCE}" "mcp:tools" e2e-agent)"
DENIED_TOKEN="$(mint "${ROUTE_RESOURCE}" "mcp:tools" other-agent)"
verify_policy_decisions() {
  local status
  status="$(echo_call "${ALLOWED_TOKEN}")"
  [ "${status}" = "200" ] || fail "the policy refused an allowed client: HTTP ${status} $(cat "${TEMP_DIR}/policy-body")"
  status="$(echo_call "${DENIED_TOKEN}")"
  [ "${status}" = "403" ] || fail "the policy let another client call echo: HTTP ${status}"
  jq --exit-status '.id == 9 and (.error.message | startswith("denied by policy"))' "${TEMP_DIR}/policy-body" >/dev/null ||
    fail "a denial is not a JSON-RPC error: $(cat "${TEMP_DIR}/policy-body")"
}
verify_policy_decisions

# Verifies SPEC-0016-R01: each client lists only the tools it may call.
log "verifying tools/list shows each client only the tools it may call"
list_tools() {
  curl --fail --silent --show-error --request POST "${GATEWAY_URL}/mcp/${SERVER_ID}" \
    --header 'Content-Type: application/json' --header "Authorization: Bearer $1" \
    --data '{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{}}' | jq --compact-output '[.result.tools[].name]'
}
[ "$(list_tools "${ALLOWED_TOKEN}")" = '["echo"]' ] || fail "the allowed client does not see echo: $(list_tools "${ALLOWED_TOKEN}")"
[ "$(list_tools "${DENIED_TOKEN}")" = '[]' ] || fail "another client sees tools it may not call: $(list_tools "${DENIED_TOKEN}")"
METRICS="$(curl --fail --silent --show-error "${GATEWAY_URL}/metrics")"
grep --quiet "^kavrynt_gateway_policy_filtered_tools_total{route=\"${SERVER_ID}\"} [1-9]" <<<"${METRICS}" ||
  fail "the Gateway did not count the filtered tool"

# Verifies SPEC-0013-R10: the production profile turns audit on; the allowed
# and the denied call each have an event with the caller, tool, and
# decision, no argument value appears, and every chain verifies.
log "verifying audit events and their chains"
STATUS="$(mcp_status)"
[ "${STATUS}" = "401" ] || fail "a call without a token answered HTTP ${STATUS}, want 401"
sleep 2
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" logs --tail=-1 --prefix=false \
  --selector app.kubernetes.io/name=gateway,app.kubernetes.io/instance=kavrynt >"${TEMP_DIR}/gateway-audit.log"
grep '"type":"kavrynt.audit.v1"' "${TEMP_DIR}/gateway-audit.log" | jq --slurp --exit-status '
  any(.[]; .client_id == "e2e-agent" and .tool == "echo" and .decision == "allow" and .outcome == "ok" and .status == 200) and
  any(.[]; .client_id == "other-agent" and .tool == "echo" and .decision == "deny" and .status == 403) and
  any(.[]; .decision == "unauthenticated" and .subject == "")' >/dev/null ||
  fail "the audit events do not record the allowed, denied, and unauthenticated calls"
if grep --fixed-strings --quiet "audit-secret-argument" "${TEMP_DIR}/gateway-audit.log"; then
  fail "a tool argument appears in the Gateway output"
fi
"${TEMP_DIR}/kavryctl" audit verify "${TEMP_DIR}/gateway-audit.log" ||
  fail "kavryctl audit verify found a break in the Gateway audit chains"

log "verifying a broken policy update keeps the last good policy"
write_policy <<'REGO'
package kavrynt.authz

decision := {
REGO
for ((i = 1; i <= 180; i++)); do
  if curl --fail --silent "${GATEWAY_URL}/metrics" | grep --quiet '^kavrynt_gateway_policy_load_failures_total [1-9]'; then
    break
  fi
  [ "${i}" -lt 180 ] || fail "the Gateway never reported the broken policy update"
  sleep 1
done
verify_policy_decisions

log "turning MCP client authentication and policy off again"
helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --reuse-values \
  --set gateway.auth.enabled=false \
  --set gateway.policy.enabled=false \
  --set gateway.policy.filterToolsList=false \
  --wait --timeout 180s >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" rollout status deployment/kavrynt-gateway --timeout=180s
stop_gateway_port_forwards
start_gateway_port_forward
wait_for_url "${GATEWAY_URL}/readyz"

log "verifying deletion cleanup"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  delete mcpserver/example-mcp-server --wait=true --timeout=120s
wait_for_json "${GATEWAY_URL}/v1/routes" '.routes | length == 0' 30

# Upgrade and rollback against the previous published release (SPEC-0009).
# The Kind node pulls that release's images from Docker Hub.
UPGRADE_FROM_VERSION="${UPGRADE_FROM_VERSION:-0.0.5-beta.1}"
UPGRADE_FROM_CHART="${UPGRADE_FROM_CHART:-oci://ghcr.io/kavrynt/charts/kavrynt}"
LOCAL_IMAGE_ARGS=(
  --kube-context "${KUBE_CONTEXT}"
  --namespace "${CONTROL_NAMESPACE}"
  --set gateway.image.repository=kavrynt/gateway
  --set-string gateway.image.tag="${LOCAL_VERSION}"
  --set operator.image.repository=kavrynt/operator
  --set-string operator.image.tag="${LOCAL_VERSION}"
)
LOCAL_RELEASE_ARGS=("${LOCAL_IMAGE_ARGS[@]}" --set gateway.auth.enabled=false)

# Verifies SPEC-0009-R05.
log "verifying helm uninstall keeps the CRD and MCPServers"
kubectl --context "${KUBE_CONTEXT}" apply -f "${TEMP_DIR}/mcp-server.yaml" >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s
stop_gateway_port_forwards
helm uninstall kavrynt --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" --wait --timeout 120s
kubectl --context "${KUBE_CONTEXT}" get crd mcpservers.kavrynt.io >/dev/null ||
  fail "helm uninstall deleted the MCPServer CRD"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" get mcpserver/example-mcp-server >/dev/null ||
  fail "helm uninstall deleted an MCPServer"

log "installing the previous release ${UPGRADE_FROM_VERSION} on a cluster without Kavrynt"
kubectl --context "${KUBE_CONTEXT}" delete crd mcpservers.kavrynt.io --wait=true --timeout=60s >/dev/null
helm install kavrynt "${UPGRADE_FROM_CHART}" --version "${UPGRADE_FROM_VERSION}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${CONTROL_NAMESPACE}" \
  --wait \
  --timeout 300s >/dev/null
kubectl --context "${KUBE_CONTEXT}" apply -f "${TEMP_DIR}/mcp-server.yaml" >/dev/null
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s
start_gateway_port_forward
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SERVER_ID}\") != null"
call_tools_list "${SERVER_ID}" || fail "tools/list failed on ${UPGRADE_FROM_VERSION}"

# Verifies SPEC-0014-R04 and SPEC-0014-R06: the previous release never set
# gateway.auth, so upgrading it without a choice fails before any change.
log "verifying an upgrade without MCP client authentication settings is refused"
GENERATIONS_BEFORE="$(deployment_generations)"
REVISION_BEFORE="$(helm history kavrynt --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" --max 1 --output json | jq '.[0].revision')"
if helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" "${LOCAL_IMAGE_ARGS[@]}" --timeout 120s \
  >"${TEMP_DIR}/unauthenticated-upgrade.log" 2>&1; then
  fail "helm upgrade succeeded without MCP client authentication settings"
fi
grep --fixed-strings --quiet "${AUTH_REFUSAL}" "${TEMP_DIR}/unauthenticated-upgrade.log" ||
  fail "the refused upgrade does not name the authentication choice: $(cat "${TEMP_DIR}/unauthenticated-upgrade.log")"
[ "$(deployment_generations)" = "${GENERATIONS_BEFORE}" ] ||
  fail "the refused upgrade changed the Gateway or Operator Deployment"
[ "$(helm history kavrynt --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" --max 1 --output json | jq '.[0].revision')" = "${REVISION_BEFORE}" ] ||
  fail "the refused upgrade recorded a new release revision"

# Verifies SPEC-0009-R07.
log "verifying an upgrade without CRD adoption fails before changing workloads"
if helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" "${LOCAL_RELEASE_ARGS[@]}" --timeout 120s \
  >"${TEMP_DIR}/unadopted-upgrade.log" 2>&1; then
  fail "helm upgrade succeeded although the CRD was not adopted"
fi
grep --fixed-strings --quiet "mcpservers.kavrynt.io" "${TEMP_DIR}/unadopted-upgrade.log" ||
  fail "the failed upgrade does not name the CRD: $(cat "${TEMP_DIR}/unadopted-upgrade.log")"
[ "$(deployment_generations)" = "${GENERATIONS_BEFORE}" ] ||
  fail "the failed upgrade changed the Gateway or Operator Deployment"

# Verifies SPEC-0009-R06 and SPEC-0009-R08: the adoption command from the
# chart README, then a normal helm upgrade.
log "adopting the CRD and upgrading to the build under test"
kubectl --context "${KUBE_CONTEXT}" label crd mcpservers.kavrynt.io \
  app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
kubectl --context "${KUBE_CONTEXT}" annotate crd mcpservers.kavrynt.io \
  meta.helm.sh/release-name=kavrynt meta.helm.sh/release-namespace="${CONTROL_NAMESPACE}" --overwrite >/dev/null
helm upgrade kavrynt "${ROOT_DIR}/charts/kavrynt" "${LOCAL_RELEASE_ARGS[@]}" --wait --timeout 180s >/dev/null
for deployment in kavrynt-gateway kavrynt-operator; do
  kubectl --context "${KUBE_CONTEXT}" -n "${CONTROL_NAMESPACE}" \
    rollout status "deployment/${deployment}" --timeout=120s
done
verify_installed_crd
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s
stop_gateway_port_forwards
start_gateway_port_forward
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SERVER_ID}\") != null"
call_tools_list "${SERVER_ID}" || fail "tools/list failed after the upgrade"

# Verifies SPEC-0009-R05 and SPEC-0009-R09.
log "rolling back to ${UPGRADE_FROM_VERSION}"
helm rollback kavrynt 1 --kube-context "${KUBE_CONTEXT}" --namespace "${CONTROL_NAMESPACE}" --wait --timeout 180s >/dev/null
kubectl --context "${KUBE_CONTEXT}" get crd mcpservers.kavrynt.io >/dev/null ||
  fail "helm rollback deleted the MCPServer CRD"
kubectl --context "${KUBE_CONTEXT}" -n "${E2E_NAMESPACE}" \
  wait mcpserver/example-mcp-server --for=condition=Ready --timeout=120s
stop_gateway_port_forwards
start_gateway_port_forward
wait_for_json "${GATEWAY_URL}/v1/routes" ".routes | map(.name) | index(\"${SERVER_ID}\") != null"
call_tools_list "${SERVER_ID}" || fail "tools/list failed after the rollback"

log "end-to-end workflow passed"
