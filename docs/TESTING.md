# Test Kavrynt: a runbook for developers

This runbook takes you from an empty Kubernetes cluster to a routed,
policy-checked, audited MCP server in about 30 minutes, and then asks for
your feedback. It works on any conformant Kubernetes cluster, local or
managed. Everything comes from published, signed images and the Helm chart;
you do not need to build anything.

| Part | What you check | Time |
| --- | --- | --- |
| [1. Get a cluster](#1-get-a-cluster) | Any Kubernetes 1.29+ | 2–5 min |
| [2. Install Kavrynt](#2-install-kavrynt) | The chart installs and both components run | 3 min |
| [3. Register an MCP server](#3-register-an-mcp-server) | The Operator validates it and discovers its tools | 3 min |
| [4. Call tools through the Gateway](#4-call-tools-through-the-gateway) | MCP routing and protocol checks | 5 min |
| [5. Open the console](#5-open-the-console) | Servers, tools, and traffic in the UI | 3 min |
| [6. Test policy and audit](#6-test-tool-authorization-policy-and-audit) | A tool is denied and hidden; every call is audited | 7 min |
| [7. Try your own MCP server](#7-try-your-own-mcp-server) | Your server, your tools | 5 min |
| [8. Clean up](#8-clean-up) | | 1 min |
| [9. Tell us what happened](#9-tell-us-what-happened) | | 5 min |

> [!WARNING]
> This runbook installs Kavrynt **without MCP client authentication**
> (`gateway.auth.enabled=false`) so you can test without an identity
> provider. Anyone who can reach the Gateway can then call every routed tool.
> Use a disposable or private cluster, and keep the Gateway Service internal.
> For a real install, see [Installing the chart](../charts/kavrynt/README.md).

## 1. Get a cluster

You need `kubectl` 1.29+ and [Helm](https://helm.sh/) 3.14+, and nodes that
can pull images from `docker.io`. The chart comes from GHCR, like in the
README; `oci://registry-1.docker.io/kavrynt/kavrynt` is a mirror of the same
chart if your network blocks `ghcr.io`. Check your tools:

```bash
kubectl version --client
helm version --short
```

Use any cluster you can create resources in, including CRDs and
ClusterRoles. If you have none, create a local one:

| Platform | Create a cluster |
| --- | --- |
| [Kind](https://kind.sigs.k8s.io/) | `kind create cluster --name kavrynt-try` |
| [k3d](https://k3d.io/) | `k3d cluster create kavrynt-try` |
| [minikube](https://minikube.sigs.k8s.io/) | `minikube start -p kavrynt-try` |
| Docker Desktop, Rancher Desktop | Enable Kubernetes in the settings |
| EKS, AKS, GKE, OpenShift, or your own | Use your cluster's kubeconfig |

Make sure `kubectl` points at the cluster you mean to use:

```bash
kubectl config current-context
kubectl auth can-i create customresourcedefinitions   # expect: yes
```

## 2. Install Kavrynt

```bash
export KAVRYNT_VERSION=1.0.0-beta.3

helm upgrade --install kavrynt oci://ghcr.io/kavrynt/charts/kavrynt \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system --create-namespace \
  --set gateway.auth.enabled=false \
  --wait --timeout 5m
```

The chart installs the `MCPServer` CRD, the Operator, and the Gateway. Check
both are running:

```bash
kubectl get deployments -n kavrynt-system
```

```text
NAME               READY   UP-TO-DATE   AVAILABLE
kavrynt-gateway    1/1     1            1
kavrynt-operator   1/1     1            1
```

**Check:** leave out `--set gateway.auth.enabled=false` on a second, dry
run. The install should stop with a message that names the authorization
server settings and the opt-out:

```bash
helm template kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version "$KAVRYNT_VERSION" >/dev/null
```

## 3. Register an MCP server

[`examples/try/sample-mcp-server.yaml`](../examples/try/sample-mcp-server.yaml)
runs a small MCP server in a new `kavrynt-try` namespace, with two tools:
`echo`, and `admin_reset`, a harmless stand-in for a dangerous tool. It runs
on the public `python` image, so nothing needs building. The file also
registers the server with Kavrynt as an `MCPServer`.

```bash
kubectl apply -f https://raw.githubusercontent.com/kavrynt/kavrynt/develop/examples/try/sample-mcp-server.yaml
kubectl -n kavrynt-try rollout status deployment/sample-mcp-server --timeout=180s
kubectl -n kavrynt-try wait mcpserver/sample --for=condition=Ready --timeout=60s
kubectl -n kavrynt-try wait mcpserver/sample --for=condition=ToolsDiscovered --timeout=120s
kubectl get mcpservers -A
```

```text
NAMESPACE     NAME     TRANSPORT   VERSION   REPORTED   READY   AGE
kavrynt-try   sample   http        1.0.0     1.0.0      True    31s
```

**Checks:**

- `READY` is `True`: the Operator validated the server and the Gateway routes
  it.
- `REPORTED` is the version the server itself reported.
- The Operator listed the tools:

  ```bash
  kubectl -n kavrynt-try get mcpserver sample -o jsonpath='{.status.tools[*].name}'; echo
  ```

  Expected: `echo admin_reset`.
- The API server rejects an unsafe endpoint at admission:

  ```bash
  kubectl -n kavrynt-try apply --dry-run=server -f - <<'EOF'
  apiVersion: kavrynt.io/v1alpha1
  kind: MCPServer
  metadata:
    name: unsafe
  spec:
    version: 0.0.1
    transport: http
    endpoint: http://user:password@sample-mcp-server:8080
  EOF
  ```

  Expected: an error saying the endpoint must be a URL without credentials.

On OpenShift, if the sample pod is refused by the restricted security context
constraint, delete the `runAsUser: 65532` line from the manifest and apply it
again.

## 4. Call tools through the Gateway

Forward the Gateway to your machine. If port 18080 is in use, pick another
one and use it in the commands below.

```bash
kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080 &
GW=http://127.0.0.1:18080/mcp/kavrynt-try.sample
```

Every server is routed at `/mcp/<namespace>.<name>`. Speak MCP to it:

```bash
# Start a session
curl -s -X POST "$GW" -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'

# List the tools
curl -s -X POST "$GW" -H 'Content-Type: application/json' -H 'MCP-Protocol-Version: 2025-06-18' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

# Call one
curl -s -X POST "$GW" -H 'Content-Type: application/json' -H 'MCP-Protocol-Version: 2025-06-18' \
  --data '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello from Kavrynt"}}}'
```

Expected for the call:
`{"jsonrpc": "2.0", "id": 3, "result": {"content": [{"type": "text", "text": "hello from Kavrynt"}]}}`.

**Checks:**

- **Malformed requests are refused before they reach the server.** A JSON-RPC
  batch gets `400`:

  ```bash
  curl -s -X POST "$GW" -H 'Content-Type: application/json' --data '[{"jsonrpc":"2.0","id":4,"method":"tools/list"}]'
  ```

- **Every call is counted in Prometheus metrics,** without arguments:

  ```bash
  curl -s http://127.0.0.1:18080/metrics | grep '^kavrynt_gateway_mcp_requests_total'
  ```

  Expect one line per method and tool, including `outcome="protocol_error"`
  for the batch.
- **The Gateway never passes your credentials on.** A call with
  `-H 'Authorization: Bearer my-token'` still works, and the token never
  reaches the MCP server.

To use the Gateway from an MCP client such as an agent framework or IDE,
point it at `http://127.0.0.1:18080/mcp/kavrynt-try.sample` with the
Streamable HTTP transport.

## 5. Open the console

Open <http://127.0.0.1:18080/ui/> and sign in as `admin` with the password
the chart generated:

```bash
kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

**Checks:**

- **Overview:** the sample server is routed. The banner that warns that MCP
  routes are not authenticated shows only when the console is opened from
  another address than this machine, so it is hidden on `127.0.0.1`; the
  Gateway logs the same warning at start:
  `kubectl -n kavrynt-system logs deploy/kavrynt-gateway | grep -i unauthenticated`.
- **Servers and Tools:** both tools appear with their input schemas.
- **Playground:** call `echo` from the browser.
- **Traffic:** your calls appear with method, tool, outcome, and latency, but
  never the arguments.

## 6. Test tool authorization policy and audit

Write a policy that denies every tool whose name starts with `admin_`:

```bash
cat > authz.rego <<'EOF'
package kavrynt.authz

default decision := {"allow": false, "reason": "admin tools are not allowed"}

decision := {"allow": true} if input.mcp_method != "tools/call"

decision := {"allow": true} if {
	input.mcp_method == "tools/call"
	not startswith(input.tool, "admin_")
}
EOF
kubectl -n kavrynt-system create configmap kavrynt-policy --from-file=authz.rego
```

Turn on the policy, the filtered tool list, and audit events:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version "$KAVRYNT_VERSION" \
  -n kavrynt-system --reset-then-reuse-values \
  --set gateway.policy.enabled=true --set gateway.policy.configMap=kavrynt-policy \
  --set gateway.policy.filterToolsList=true --set gateway.audit.enabled=true \
  --wait --timeout 3m
```

The Gateway restarts, so start the port-forward from step 4 again. Then:

```bash
# admin_reset is denied with a JSON-RPC error
curl -s -w ' HTTP %{http_code}\n' -X POST "$GW" -H 'Content-Type: application/json' -H 'MCP-Protocol-Version: 2025-06-18' \
  --data '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"admin_reset","arguments":{}}}'

# echo still works
curl -s -X POST "$GW" -H 'Content-Type: application/json' -H 'MCP-Protocol-Version: 2025-06-18' \
  --data '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo","arguments":{"message":"still allowed"}}}'

# tools/list now shows only echo
curl -s -X POST "$GW" -H 'Content-Type: application/json' -H 'MCP-Protocol-Version: 2025-06-18' \
  --data '{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}'
```

Expected for the denied call:
`{"error":{"code":-32001,"message":"denied by policy: admin tools are not allowed"},...} HTTP 200`.
MCP client SDKs show the agent that message. Releases up to 1.0.0-beta.3
answer `HTTP 403` with the same body.

**Audit:** every MCP request is now one JSON line on the Gateway's output,
chained with SHA-256. Install `kavryctl` and verify the chain:

```bash
curl -fsSL https://raw.githubusercontent.com/kavrynt/kavrynt/v1.0.0-beta.3/scripts/install.sh \
  | KAVRYNT_VERSION=v1.0.0-beta.3 sh
export PATH="$HOME/.kavrynt/bin:$PATH"

kubectl -n kavrynt-system logs -l app.kubernetes.io/name=gateway --prefix=false --tail=-1 \
  | kavryctl audit verify
```

Expected: `... every chain verifies`.

To see the events themselves:

```bash
kubectl -n kavrynt-system logs -l app.kubernetes.io/name=gateway --prefix=false --tail=-1 \
  | grep '"type":"kavrynt.audit.v1"' | tail -3
```

They show the decision (`deny` for `admin_reset`, `allow` for `echo`), the
tool, and the outcome, never the arguments.

To test **client authentication**, you need an OAuth 2.1 authorization
server (for example Keycloak, Auth0, Okta, or Entra ID) that issues JWT
access tokens. See
[Authenticating MCP clients](../charts/kavrynt/README.md#authenticating-mcp-clients).
Policies can then decide per caller (`input.client_id`, `input.subject`).

## 7. Try your own MCP server

Any MCP server that speaks **Streamable HTTP** works; `stdio` servers are not
routed yet. Run it in the cluster as a normal Deployment and Service, then
register it:

```bash
kubectl apply -f - <<'EOF'
apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: my-server
  namespace: default
spec:
  version: 0.1.0
  transport: http
  endpoint: http://my-server.default.svc.cluster.local:8080/mcp   # your Service and MCP path
EOF
kubectl get mcpservers -A
kubectl describe mcpserver my-server
```

`READY True` means the Gateway routes the server, not that it answers.
Check the `DISCOVERY` column: if it is `DiscoveryFailed`, the
`ToolsDiscovered` message in `kubectl describe` names the cause and what to
check, for example no ready pods behind the Service, a host that does not
resolve, or a wrong path (`HTTP 404 for path "/"`; many MCP servers serve at
`/mcp`). See [Discovery messages](components/operator/README.md#discovery-messages).
If the server needs an API key, see `spec.credential` in the
[Gateway docs](components/gateway/README.md#upstream-credentials).

If the server keeps MCP sessions in memory (most servers built with the
official SDKs do) and runs more than one replica, calls fail with
`session_not_found`. Run one replica, or, from the release after
`1.0.0-beta.2` (whose CRD has no `sessionAffinity`), when the endpoint is an `http`
Service name and the Service has one port, add
`sessionAffinity: MCPSession` to the spec; it is not valid for `https`
endpoints, and multi-port Services get `503`. See
[session affinity](components/gateway/README.md#session-affinity).

## 8. Clean up

```bash
kill %1 2>/dev/null                    # the port-forward
kubectl delete namespace kavrynt-try
helm uninstall kavrynt -n kavrynt-system
kubectl delete crd mcpservers.kavrynt.io   # Helm keeps it on purpose
kubectl delete namespace kavrynt-system
```

Or delete the whole local cluster, for example
`kind delete cluster --name kavrynt-try`.

## 9. Tell us what happened

Your feedback decides what we build next. Please
[open a tester feedback issue](https://github.com/kavrynt/kavrynt/issues/new?template=tester_feedback.yml),
even if everything worked. It helps to include:

- the output of `kavryctl doctor --anonymize` (kavryctl 1.0.0-beta.4 or
  later): a read-only check of the installation and its MCP servers, with
  what to do for each problem and no secrets, endpoints, or names;
- your Kubernetes platform and version, and the Kavrynt version;
- which steps worked and which did not, with the command and its output;
- for a problem, `kubectl get mcpservers -A -o yaml` and
  `kubectl -n kavrynt-system logs deploy/kavrynt-gateway --tail=100`, with
  secrets removed;
- what you would need before using Kavrynt for real.

Report security vulnerabilities privately instead:
[security advisories](https://github.com/kavrynt/kavrynt/security/advisories/new).
