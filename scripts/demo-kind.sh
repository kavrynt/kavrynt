#!/usr/bin/env bash
# Kavrynt demo on a disposable Kind cluster. See docs/DEMO.md.
#
#   demo-kind.sh up           build local images, create the cluster, install
#   demo-kind.sh walkthrough  paced, narrated product loop (Enter advances)
#   demo-kind.sh forward      port-forward the Gateway for the console
#   demo-kind.sh down         delete the cluster this script created
#
# The cluster gets its own kubeconfig under .kavrynt/demo; the caller's
# current Kubernetes context is never read or changed.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${KIND_CLUSTER_NAME:-kavrynt-demo}"
STATE_DIR="${ROOT_DIR}/.kavrynt/demo"
MARKER_FILE="${STATE_DIR}/cluster"
CONTROL_NAMESPACE="kavrynt-system"
DEMO_DIR="${ROOT_DIR}/examples/demo"
DEMO_PORT="${DEMO_PORT:-18080}"
DEMO_AUTO="${DEMO_AUTO:-0}"
CHART_VERSION="$(awk '$1 == "version:" { print $2; exit }' "${ROOT_DIR}/charts/kavrynt/Chart.yaml")"
LOCAL_VERSION="${LOCAL_VERSION:-${CHART_VERSION}-local}"

GATEWAY_IMAGE="kavrynt/gateway:${LOCAL_VERSION}"
OPERATOR_IMAGE="kavrynt/operator:${LOCAL_VERSION}"
DEMO_IMAGE="kavrynt/demo-mcp-server:${LOCAL_VERSION}"

export KUBECONFIG="${STATE_DIR}/kubeconfig"
export PATH="${STATE_DIR}/bin:${PATH}"
GW="http://127.0.0.1:${DEMO_PORT}"
PORT_FORWARD_PID=""
# Console session for the walkthrough's curl calls to /v1/traffic.
CONSOLE_COOKIES=".kavrynt/demo/console-cookies"
PASSWORD_COMMAND="kubectl -n ${CONTROL_NAMESPACE} get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d"

if [ -t 1 ]; then
  BOLD=$'\033[1m' DIM=$'\033[2m' CYAN=$'\033[36m' YELLOW=$'\033[33m' RESET=$'\033[0m'
else
  BOLD="" DIM="" CYAN="" YELLOW="" RESET=""
fi

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

cluster_exists() {
  kind get clusters 2>/dev/null | grep --fixed-strings --line-regexp "${CLUSTER_NAME}" >/dev/null
}

owns_cluster() {
  [ -f "${MARKER_FILE}" ] && [ "$(cat "${MARKER_FILE}")" = "${CLUSTER_NAME}" ]
}

require_demo_cluster() {
  owns_cluster && cluster_exists && [ -f "${KUBECONFIG}" ] ||
    fail "no demo cluster ${CLUSTER_NAME}; run 'make demo-up' first"
}

cleanup() {
  local exit_code=$?
  set +e
  if [ -n "${PORT_FORWARD_PID}" ]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1
    wait "${PORT_FORWARD_PID}" >/dev/null 2>&1
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

cmd_up() {
  local command archive commit build_date
  for command in docker kind kubectl helm go; do
    require_command "${command}"
  done
  docker info >/dev/null 2>&1 || fail "Docker is not reachable"

  if cluster_exists && ! owns_cluster; then
    fail "Kind cluster ${CLUSTER_NAME} exists but was not created by this script; set KIND_CLUSTER_NAME"
  fi

  # Helm installs every chart under charts/, so an old `helm dependency build`
  # package would deploy next to the vendored subcharts.
  for archive in "${ROOT_DIR}"/charts/kavrynt/charts/*.tgz; do
    [ -e "${archive}" ] || continue
    fail "stale packaged subchart ${archive}; subcharts are vendored as directories"
  done

  mkdir -p "${STATE_DIR}/bin"

  log "building kavryctl"
  (cd "${ROOT_DIR}" && CGO_ENABLED=0 go build -trimpath -o "${STATE_DIR}/bin/kavryctl" ./cmd/kavryctl)

  log "building images ${LOCAL_VERSION}"
  commit="$(git -C "${ROOT_DIR}" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
  build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for target in gateway operator; do
    docker build --quiet \
      --file "${ROOT_DIR}/build/Dockerfile" \
      --target "${target}" \
      --build-arg VERSION="${LOCAL_VERSION}" \
      --build-arg COMMIT="${commit}" \
      --build-arg BUILD_DATE="${build_date}" \
      --tag "kavrynt/${target}:${LOCAL_VERSION}" \
      "${ROOT_DIR}" >/dev/null
  done
  docker build --quiet --tag "${DEMO_IMAGE}" "${DEMO_DIR}/mcp-server" >/dev/null

  if cluster_exists; then
    log "reusing Kind cluster ${CLUSTER_NAME}"
    kind export kubeconfig --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG}" >/dev/null 2>&1
  else
    log "creating Kind cluster ${CLUSTER_NAME}"
    printf '%s' "${CLUSTER_NAME}" >"${MARKER_FILE}"
    kind create cluster --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG}" --wait 120s
  fi

  log "loading images into Kind"
  kind load docker-image --name "${CLUSTER_NAME}" "${GATEWAY_IMAGE}" "${OPERATOR_IMAGE}" "${DEMO_IMAGE}"

  log "installing the Kavrynt control plane"
  # The chart manages the CRD. A reused cluster may hold one installed by an
  # older chart, which Helm adopts only with its ownership metadata.
  if kubectl get crd mcpservers.kavrynt.io >/dev/null 2>&1; then
    kubectl label crd mcpservers.kavrynt.io app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
    kubectl annotate crd mcpservers.kavrynt.io \
      meta.helm.sh/release-name=kavrynt meta.helm.sh/release-namespace="${CONTROL_NAMESPACE}" --overwrite >/dev/null
  fi
  helm upgrade --install kavrynt "${ROOT_DIR}/charts/kavrynt" \
    --namespace "${CONTROL_NAMESPACE}" \
    --create-namespace \
    --set gateway.image.repository=kavrynt/gateway \
    --set-string gateway.image.tag="${LOCAL_VERSION}" \
    --set operator.image.repository=kavrynt/operator \
    --set-string operator.image.tag="${LOCAL_VERSION}" \
    --wait \
    --timeout 180s >/dev/null
  # Pick up rebuilt images that kept the same tag.
  kubectl -n "${CONTROL_NAMESPACE}" rollout restart deployment/kavrynt-gateway deployment/kavrynt-operator >/dev/null
  for deployment in kavrynt-gateway kavrynt-operator; do
    kubectl -n "${CONTROL_NAMESPACE}" rollout status "deployment/${deployment}" --timeout=120s
  done

  log "deploying the demo MCP servers"
  sed "s|__DEMO_IMAGE__|${DEMO_IMAGE}|g" "${DEMO_DIR}/manifests/workloads.yaml" | kubectl apply -f - >/dev/null
  reset_demo_state

  cat <<EOF

Demo cluster ${CLUSTER_NAME} is ready. No MCP servers are registered yet.

  make demo-walkthrough        run the narrated walkthrough (serves ${GW}/ui/)
  export KUBECONFIG=${KUBECONFIG}
                               use kubectl and kavryctl against the demo cluster
  make demo-down               delete the cluster

EOF
}

# reset_demo_state removes every registration and restarts the demo servers,
# whose state is in memory, so each walkthrough starts from the same place.
reset_demo_state() {
  kubectl delete mcpservers --all -n team-sre --wait=true >/dev/null 2>&1 || true
  kubectl delete mcpservers --all -n team-platform --wait=true >/dev/null 2>&1 || true
  kubectl -n team-sre rollout restart deployment/incidents >/dev/null
  kubectl -n team-platform rollout restart deployment/runbooks >/dev/null
  kubectl -n team-sre rollout status deployment/incidents --timeout=120s >/dev/null
  kubectl -n team-platform rollout status deployment/runbooks --timeout=120s >/dev/null
}

# start_port_forward serves the Gateway on DEMO_PORT, the URL the audience
# sees in the browser. It fails rather than pick another port: a fixed port
# may belong to another cluster whose Gateway would then answer.
start_port_forward() {
  local log_file="${STATE_DIR}/port-forward.log"
  local i
  kubectl -n "${CONTROL_NAMESPACE}" port-forward --address 127.0.0.1 \
    service/kavrynt-gateway "${DEMO_PORT}:8080" >"${log_file}" 2>&1 &
  PORT_FORWARD_PID=$!
  for ((i = 1; i <= 30; i++)); do
    if ! kill -0 "${PORT_FORWARD_PID}" 2>/dev/null; then
      PORT_FORWARD_PID=""
      fail "Gateway port-forward on ${DEMO_PORT} failed (port in use? set DEMO_PORT): $(cat "${log_file}")"
    fi
    if curl --fail --silent "${GW}/readyz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "Gateway did not become reachable at ${GW}"
}

# console_login signs the walkthrough in to the console API, as the browser
# does, with the generated admin password.
console_login() {
  kubectl -n "${CONTROL_NAMESPACE}" get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' |
    base64 --decode |
    jq --raw-input '{username: "admin", password: .}' |
    curl --fail --silent --show-error --cookie-jar "${ROOT_DIR}/${CONSOLE_COOKIES}" \
      --header 'Content-Type: application/json' --data @- "${GW}/v1/session" ||
    fail "console login with the generated admin password failed"
}

cmd_forward() {
  require_demo_cluster
  log "Gateway on ${GW} (console ${GW}/ui/, user admin, password: ${PASSWORD_COMMAND}); Ctrl-C to stop"
  exec kubectl -n "${CONTROL_NAMESPACE}" port-forward --address 127.0.0.1 \
    service/kavrynt-gateway "${DEMO_PORT}:8080"
}

cmd_down() {
  if ! owns_cluster; then
    fail "no demo cluster recorded in ${MARKER_FILE}; refusing to delete anything"
  fi
  if cluster_exists; then
    log "deleting Kind cluster ${CLUSTER_NAME}"
    kind delete cluster --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG}"
  fi
  rm -rf "${STATE_DIR}"
}

# --- walkthrough helpers ---------------------------------------------------

STEP=0
LAST_OUTPUT=""

step() {
  STEP=$((STEP + 1))
  printf '\n%s%s── %d. %s%s\n' "${BOLD}" "${CYAN}" "${STEP}" "$1" "${RESET}"
  if [ $# -gt 1 ]; then
    printf '%s%s%s\n' "${DIM}" "$2" "${RESET}"
  fi
}

say() {
  printf '%s%s%s\n' "${YELLOW}" "$*" "${RESET}"
}

pause() {
  if [ "${DEMO_AUTO}" = "1" ]; then
    return 0
  fi
  printf '%s[Enter]%s' "${DIM}" "${RESET}"
  read -r _ </dev/tty
}

# run prints a command (read from stdin, $GW expanded for display), runs it,
# and keeps its output in LAST_OUTPUT. It fails the walkthrough when the
# command fails, unless called as `run expect-fail`.
run() {
  local expect_fail=0 cmd status
  [ "${1:-}" = "expect-fail" ] && expect_fail=1
  cmd="$(cat)"
  printf '%s$ %s%s\n' "${BOLD}" "${cmd//\$GW/${GW}}" "${RESET}"
  set +e
  LAST_OUTPUT="$(eval "${cmd}" 2>&1)"
  status=$?
  set -e
  [ -n "${LAST_OUTPUT}" ] && printf '%s\n' "${LAST_OUTPUT}"
  if [ "${expect_fail}" -eq 0 ] && [ "${status}" -ne 0 ]; then
    fail "command failed with status ${status}"
  fi
  if [ "${expect_fail}" -eq 1 ] && [ "${status}" -eq 0 ]; then
    fail "command succeeded but was expected to fail"
  fi
}

expect() {
  grep --fixed-strings --quiet -- "$1" <<<"${LAST_OUTPUT}" ||
    fail "expected output to contain: $1"
}

expect_not() {
  if grep --fixed-strings --quiet -- "$1" <<<"${LAST_OUTPUT}"; then
    fail "expected output not to contain: $1"
  fi
}

# mcp is the JSON-RPC call an agent makes through the Gateway.
mcp() {
  curl --silent --show-error --fail "${GW}/mcp/$1" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d "$2"
}

cmd_walkthrough() {
  for command in kubectl curl jq kavryctl; do
    require_command "${command}"
  done
  require_demo_cluster
  cd "${ROOT_DIR}"

  log "resetting demo state"
  reset_demo_state
  start_port_forward
  console_login

  printf '\n%sKavrynt: a Kubernetes-native control plane for MCP servers%s\n' "${BOLD}" "${RESET}"
  printf 'Gateway: %s   Console: %s/ui/\n' "${GW}" "${GW}"
  pause

  step "The control plane" "One Helm chart: an Operator, a Gateway, and the MCPServer CRD."
  run <<'CMD'
kubectl -n kavrynt-system get deployments,services
CMD
  run <<'CMD'
kubectl get crd mcpservers.kavrynt.io
CMD
  say "The console has an admin login, like Argo CD. Helm generated the password:"
  run <<'CMD'
kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d; echo
CMD
  say "Sign in at $GW/ui/ as admin. The login protects the console; MCP routes stay open to anyone who can reach the Gateway."
  pause

  step "Two teams, two MCP servers, no routes" "Plain Deployments and Services. Kavrynt does not run them; it registers and routes to them."
  run <<'CMD'
kubectl get deployments -n team-sre; kubectl get deployments -n team-platform
CMD
  run <<'CMD'
curl -s $GW/v1/routes | jq
CMD
  expect '"routes": []'
  say "Open ${GW}/ui/ now: the console is empty."
  pause

  step "Register declaratively with kubectl" "An MCPServer is a Kubernetes resource: GitOps, RBAC, and kubectl apply all work."
  run <<'CMD'
cat examples/demo/manifests/incidents.yaml
CMD
  run <<'CMD'
kubectl apply -f examples/demo/manifests/incidents.yaml
CMD
  run <<'CMD'
kubectl wait -n team-sre mcpserver/incidents --for=condition=ToolsDiscovered --timeout=60s
CMD
  pause

  step "Register with kavryctl" "The same API, for developers who do not write YAML by hand."
  run <<'CMD'
kavryctl validate examples/demo/manifests/runbooks.yaml
CMD
  run <<'CMD'
kavryctl register -n team-platform examples/demo/manifests/runbooks.yaml
CMD
  run <<'CMD'
kubectl wait -n team-platform mcpserver/runbooks --for=condition=ToolsDiscovered --timeout=60s
CMD
  run <<'CMD'
kavryctl list -A
CMD
  expect "/mcp/team-sre.incidents"
  expect "/mcp/team-platform.runbooks"
  pause

  step "The Operator discovered the tools" "It ran MCP initialize + tools/list against each Ready server and wrote the result to status."
  run <<'CMD'
kubectl get mcpserver incidents -n team-sre -o json | jq '{conditions: [.status.conditions[] | {type, status, reason}], tools: [.status.tools[].name]}'
CMD
  expect "acknowledge_incident"
  say "Refresh the console: both servers, their conditions, and their tools."
  pause

  step "An agent calls tools through the Gateway" "One URL per server, namespace-qualified: /mcp/<namespace>.<name>."
  run <<'CMD'
mcp team-sre.incidents '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_incidents","arguments":{"status":"open"}}}' | jq -r '.result.content[0].text'
CMD
  expect "INC-1043"
  run <<'CMD'
mcp team-platform.runbooks '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_runbook","arguments":{"id":"RB-3"}}}' | jq -r '.result.content[0].text'
CMD
  expect "CrashLoopBackOff"
  run <<'CMD'
mcp team-sre.incidents '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_incident","arguments":{"id":"INC-9999"}}}' | jq '.result'
CMD
  expect '"isError": true'
  say "Any MCP client works the same way, for example:"
  say "  claude mcp add --transport http incidents ${GW}/mcp/team-sre.incidents"
  pause

  step "Live traffic" "The Gateway records method, tool, outcome, and latency. Never tool arguments."
  run <<'CMD'
curl -s -b .kavrynt/demo/console-cookies $GW/v1/traffic | jq '[.requests[] | {route, method, tool, outcome}] | .[:5]'
CMD
  expect "tool_error"
  run <<'CMD'
curl -s $GW/metrics | grep '^kavrynt_gateway_mcp_requests_total'
CMD
  expect 'tool="list_incidents"'
  say "Console > Traffic shows the same calls; Playground calls tools from the browser."
  pause

  step "Guardrails that exist today" "Defense in depth while authentication is still on the roadmap."
  say "a) The CRD rejects endpoints with embedded credentials at admission:"
  run expect-fail <<'CMD'
kubectl apply --dry-run=server -f - <<'EOF'
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata: {name: leaky, namespace: team-sre}
spec: {version: 0.1.0, transport: http, endpoint: "http://admin:hunter2@incidents.team-sre:8080"}
EOF
CMD
  say "b) Caller credentials never reach the MCP server:"
  run <<'CMD'
curl -s $GW/mcp/team-sre.incidents/debug/headers -H 'Authorization: Bearer agent-token' -H 'Cookie: session=1' | jq 'keys'
CMD
  expect_not "Authorization"
  expect_not "Cookie"
  say "c) Cross-origin browser requests are refused:"
  run <<'CMD'
curl -s -o /dev/null -w '%{http_code}\n' $GW/mcp/team-sre.incidents -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
CMD
  expect "403"
  say "d) A server pointing at cloud metadata is registered, but the Gateway refuses to dial it:"
  run <<'CMD'
kubectl apply -f - <<'EOF'
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata: {name: metadata, namespace: team-sre}
spec: {version: 0.1.0, transport: http, endpoint: "http://169.254.169.254/latest/meta-data/"}
EOF
CMD
  local i
  for ((i = 1; i <= 30; i++)); do
    curl --fail --silent "${GW}/v1/routes" | jq --exit-status '.routes | map(.name) | index("team-sre.metadata") != null' >/dev/null 2>&1 && break
    sleep 1
  done
  run <<'CMD'
curl -s -o /dev/null -w '%{http_code}\n' $GW/mcp/team-sre.metadata
CMD
  expect "502"
  run <<'CMD'
kubectl delete mcpserver metadata -n team-sre
CMD
  say "e) The Gateway is read-only on MCPServers and cannot read Secrets:"
  run <<'CMD'
for verb in list delete; do printf '%s mcpservers: ' $verb; kubectl auth can-i $verb mcpservers.kavrynt.io -A --as=system:serviceaccount:kavrynt-system:kavrynt-gateway; done; printf 'get secrets: '; kubectl auth can-i get secrets -A --as=system:serviceaccount:kavrynt-system:kavrynt-gateway || true
CMD
  expect "list mcpservers: yes"
  expect "delete mcpservers: no"
  expect "get secrets: no"
  pause

  step "The caveat, live" "acknowledge_incident changes state, and nothing stops this call today."
  run <<'CMD'
mcp team-sre.incidents '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"acknowledge_incident","arguments":{"id":"INC-1043","note":"ack from an unauthenticated client"}}}' | jq -r '.result.content[0].text'
CMD
  expect "INC-1043 acknowledged"
  say "No client identity, no tool policy, no audit record. That is ADR-0003, next on the roadmap."
  pause

  step "Lifecycle" "Unregister, and the route disappears from the Gateway."
  run <<'CMD'
kavryctl unregister -n team-platform runbooks
CMD
  for ((i = 1; i <= 30; i++)); do
    curl --fail --silent "${GW}/v1/routes" | jq --exit-status '.routes | map(.name) | index("team-platform.runbooks") == null' >/dev/null 2>&1 && break
    sleep 1
  done
  run <<'CMD'
curl -s $GW/v1/routes | jq '[.routes[].name]'
CMD
  expect_not "team-platform.runbooks"
  pause

  step "What Kavrynt does not do yet"
  cat <<'EOF'
  - No client authentication or token exchange: anyone who reaches the Gateway
    can call every routed tool. The console has no login.
  - No policy or tool-level authorization, and no audit log.
  - HTTP MCP servers only; stdio manifests validate but are not proxied.
  - Single replica; no tested high-availability profile.
  - 0.0.x beta, one maintainer organization. Run it in trusted dev or
    evaluation clusters only.
EOF
  pause

  if [ "${DEMO_AUTO}" = "1" ]; then
    log "walkthrough passed"
    return 0
  fi
  printf '\nThe console stays up at %s/ui/. Press Enter to stop the port-forward.' "${GW}"
  read -r _ </dev/tty
}

case "${1:-}" in
  up) cmd_up ;;
  walkthrough) cmd_walkthrough ;;
  forward) cmd_forward ;;
  down) cmd_down ;;
  *) fail "usage: $(basename "$0") up|walkthrough|forward|down" ;;
esac
