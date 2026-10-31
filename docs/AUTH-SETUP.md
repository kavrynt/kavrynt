# Setting Up MCP Client Authentication

With MCP client authentication on (the chart's default), every call to
`/mcp/<namespace>.<name>` needs a bearer token from an authorization server
the Gateway trusts. This guide explains what that server must issue, how to
choose `gateway.auth.resourceURL`, and walks through Keycloak end to end.
The token rules are in
[Gateway: MCP client authentication](components/gateway/README.md#mcp-client-authentication).

## What the Gateway needs

Each MCP call must carry a JWT access token that:

- is signed with an asymmetric key published in the issuer's key set;
- has `iss` exactly equal to an issuer in `gateway.auth.issuers`;
- has an `aud` that contains the route's **resource URI**,
  `<resourceURL>/mcp/<namespace>.<name>`;
- carries every scope in `gateway.auth.requiredScopes`.

The audience is per route. A token for one route is refused on every other
route, so a stolen token reaches one MCP server, not all of them.

Authorization servers that support RFC 8707 resource indicators put the
requested `resource` into `aud`. Others, including Keycloak, need the
audience configured on the client, once per route the client may call (see
[Many servers per agent](#many-servers-per-agent)).

## Choosing `resourceURL`

`gateway.auth.resourceURL` is the `https` origin agents use to reach the
Gateway, without a path, for example `https://mcp.example.com`. It must be
`https`.

The Gateway only compares it with the tokens' `aud`; it never connects to
it. For a local test behind a plain-http port-forward, pick an `https` name
that does not need to resolve, such as `https://mcp.dev.local`, configure
the audiences with it, and call the Gateway at `http://127.0.0.1:18080`.
Clients that run the OAuth flow themselves compare the `resource` of the
route's metadata with the URL they call, so they may refuse the mismatch.
For a local test, get the token yourself (as below) and send it in the
`Authorization` header. In production, use the name agents really call.

## Keycloak

Tested with Keycloak 26.4 and the sample server of the
[testing runbook](TESTING.md) (`kavrynt-try.sample`). It runs Keycloak in
development mode in the cluster, so treat it as a lab: the passwords below
are placeholders. The commands need `curl`, `jq`, and `python3`.

### 1. Run Keycloak

```bash
kubectl create namespace idp
kubectl -n idp apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: keycloak}
spec:
  selector: {matchLabels: {app: keycloak}}
  template:
    metadata: {labels: {app: keycloak}}
    spec:
      containers:
      - name: keycloak
        image: quay.io/keycloak/keycloak:26.4
        args: ["start-dev", "--http-port=8080"]
        env:
        - {name: KC_BOOTSTRAP_ADMIN_USERNAME, value: admin}
        - {name: KC_BOOTSTRAP_ADMIN_PASSWORD, value: admin}
        # Tokens name this issuer even when fetched through a port-forward,
        # so it matches the issuer the Gateway trusts.
        - {name: KC_HOSTNAME, value: "http://keycloak.idp.svc.cluster.local:8080"}
        - {name: KC_HOSTNAME_BACKCHANNEL_DYNAMIC, value: "true"}
        readinessProbe: {httpGet: {path: /realms/master, port: 8080}, periodSeconds: 5}
---
apiVersion: v1
kind: Service
metadata: {name: keycloak}
spec:
  selector: {app: keycloak}
  ports: [{port: 8080, targetPort: 8080}]
EOF
kubectl -n idp rollout status deploy/keycloak --timeout=8m
kubectl -n idp port-forward svc/keycloak 18180:8080 &
```

`KC_HOSTNAME` matters: without it, a token fetched through
`127.0.0.1:18180` carries `iss: http://127.0.0.1:18180/realms/agents`, which
the Gateway does not trust, and every call answers `401`.

### 2. Create a realm, a scope, and a client for the agent

The agent is a confidential client using client credentials. An audience
mapper adds the route's resource URI to its tokens.

```bash
KC=http://127.0.0.1:18180
RESOURCE=https://mcp.dev.local/mcp/kavrynt-try.sample
ADM=$(curl -s $KC/realms/master/protocol/openid-connect/token \
  -d grant_type=password -d client_id=admin-cli -d username=admin -d password=admin | jq -r .access_token)
A=(-H "Authorization: Bearer $ADM" -H 'Content-Type: application/json')

curl -s "${A[@]}" $KC/admin/realms \
  -d '{"realm":"agents","enabled":true,"accessTokenLifespan":600}'
curl -s "${A[@]}" $KC/admin/realms/agents/client-scopes \
  -d '{"name":"mcp:tools","protocol":"openid-connect","attributes":{"include.in.token.scope":"true"}}'
curl -s "${A[@]}" $KC/admin/realms/agents/clients -d '{
  "clientId": "research-agent", "secret": "agent-secret",
  "publicClient": false, "serviceAccountsEnabled": true, "standardFlowEnabled": false,
  "protocolMappers": [{
    "name": "sample-route-audience", "protocol": "openid-connect",
    "protocolMapper": "oidc-audience-mapper",
    "config": {"included.custom.audience": "'"$RESOURCE"'", "access.token.claim": "true"}}]}'

CID=$(curl -s "${A[@]}" "$KC/admin/realms/agents/clients?clientId=research-agent" | jq -r '.[0].id')
SID=$(curl -s "${A[@]}" $KC/admin/realms/agents/client-scopes | jq -r '.[] | select(.name=="mcp:tools") | .id')
curl -s -X PUT "${A[@]}" $KC/admin/realms/agents/clients/$CID/default-client-scopes/$SID
```

Check a token:

```bash
TOKEN=$(curl -s $KC/realms/agents/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=research-agent -d client_secret=agent-secret | jq -r .access_token)
python3 -c 'import sys, base64, json; p = sys.argv[1].split(".")[1]; p += "=" * (-len(p) % 4); t = json.loads(base64.urlsafe_b64decode(p)); print({k: t.get(k) for k in ("iss", "aud", "scope")})' "$TOKEN"
```

`iss` is `http://keycloak.idp.svc.cluster.local:8080/realms/agents`, `aud`
contains `$RESOURCE`, and `scope` contains `mcp:tools`.

### 3. Trust Keycloak in the Gateway

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version "$KAVRYNT_VERSION" \
  -n kavrynt-system --reset-then-reuse-values \
  --set gateway.auth.enabled=true \
  --set gateway.auth.resourceURL=https://mcp.dev.local \
  --set-json 'gateway.auth.issuers=[{"issuer":"http://keycloak.idp.svc.cluster.local:8080/realms/agents","allowHTTP":true}]' \
  --set 'gateway.auth.requiredScopes={mcp:tools}' \
  --wait --timeout 3m
```

`allowHTTP` is needed only because this Keycloak serves plain http inside
the cluster; the Gateway logs a warning for it. Use an https issuer in
production.

### 4. Call through the Gateway

```bash
kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080 &
GW=http://127.0.0.1:18080/mcp/kavrynt-try.sample
H=(-H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream')
BODY='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

# Without a token: 401, and WWW-Authenticate names the route's metadata.
curl -s -D - -o /dev/null -X POST $GW "${H[@]}" --data "$BODY" | grep -iE '^HTTP|www-authenticate'
curl -s http://127.0.0.1:18080/.well-known/oauth-protected-resource/mcp/kavrynt-try.sample

# With the token: the tools.
curl -s -X POST $GW "${H[@]}" -H "Authorization: Bearer $TOKEN" --data "$BODY"
```

The same token on another route answers `401` with
`error="invalid_token"`: its audience is only this route.

### Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `401` `invalid_token` with a fresh token | `iss` differs from the trusted issuer (check `KC_HOSTNAME`), or `aud` lacks the route's resource URI (check the mapper's audience against `resourceURL`) |
| `403` `insufficient_scope` | The client lacks the `mcp:tools` scope, or the scope is not in the token (`include.in.token.scope`) |
| `503` with `Retry-After` | The Gateway cannot load Keycloak's keys: check the issuer URL, `allowHTTP`, and that the Gateway can reach the Service |
| The chart refuses the values | `resourceURL` is not `https`, or has a path |

## Console sign-in with Keycloak

The console can use Keycloak for people too (SPEC-0032). This was not yet
run against a live provider; the unit tests cover the flow. Before you
start:

- **Keycloak must be reachable at an `https` URL** from people's browsers,
  for example through an Ingress, and `KC_HOSTNAME` must be that URL. The
  lab of step 1 (`http://keycloak.idp.svc.cluster.local:8080`) works for
  agents' tokens but not here: the browser cannot reach it, and its ID
  tokens would name an `http` issuer that differs from the one below.
- **Use the `$KC` and `A` variables of step 2** (an admin token for that
  Keycloak) in the commands below.

Create a confidential client with the standard flow and the Gateway's
callback as its redirect URL, and a group mapper so ID tokens carry
`groups`:

```bash
GATEWAY=https://mcp.example.com      # the console's external URL
curl -s "${A[@]}" $KC/admin/realms/agents/clients -d '{
  "clientId": "kavrynt-console", "secret": "console-secret",
  "publicClient": false, "standardFlowEnabled": true, "serviceAccountsEnabled": false,
  "redirectUris": ["'"$GATEWAY"'/v1/session/oidc/callback"],
  "attributes": {"pkce.code.challenge.method": "S256"},
  "protocolMappers": [{"name": "groups", "protocol": "openid-connect",
    "protocolMapper": "oidc-group-membership-mapper",
    "config": {"claim.name": "groups", "full.path": "false", "id.token.claim": "true"}}]}'
kubectl -n kavrynt-system create secret generic kavrynt-console-oidc --from-literal=client-secret=console-secret
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --version "$KAVRYNT_VERSION" \
  -n kavrynt-system --reset-then-reuse-values \
  --set gateway.console.auth.oidc.enabled=true \
  --set gateway.console.auth.oidc.issuer=https://keycloak.example.com/realms/agents \
  --set gateway.console.auth.oidc.clientID=kavrynt-console \
  --set gateway.console.auth.oidc.clientSecret.name=kavrynt-console-oidc \
  --set gateway.console.auth.oidc.redirectURL=$GATEWAY/v1/session/oidc/callback \
  --set gateway.console.auth.oidc.providerName=Keycloak \
  --set 'gateway.console.auth.oidc.allowedGroups={platform}' \
  --set "gateway.config.allowedOrigins={$GATEWAY}"
```

Set `issuer` to `KC_HOSTNAME` followed by `/realms/agents`, exactly as it
appears in the ID tokens' `iss`. `allowedOrigins` lets the console's own
requests (log out, playground) through at a non-loopback address; the
`--set` replaces the list, so name any origins you already allow too. Add people to the `platform`
group in Keycloak; others get "not allowed". The admin password then stops
working unless `keepAdminLogin` is set.

## Many servers per agent

Each route has its own audience, so an agent that calls ten MCP servers
needs a token whose `aud` lists all ten resource URIs. With Keycloak, add
one audience mapper per route to the agent's client (or to a client scope
shared by the agents that may call that route). A token's `aud` then lists
every route the client may call, and the token works on each of them.

Keep the lists small: each audience is a route the token can reach if it
leaks. Per-route audiences are how the Gateway limits a token to the
servers an agent should use.

### One audience for a group of routes

When per-route audiences are too much to manage, or your authorization
server issues one audience per token, turn on the Gateway audience
(SPEC-0031) for the routes one token should reach:

```bash
helm upgrade kavrynt oci://ghcr.io/kavrynt/charts/kavrynt --reuse-values \
  --version "$KAVRYNT_VERSION" \
  --namespace kavrynt-system \
  --set gateway.auth.gatewayAudience.enabled=true \
  --set 'gateway.auth.gatewayAudience.routes={team-a.*,team-c.reports}'
```

Those routes then also accept a token whose `aud` is `resourceURL` itself,
for example `https://mcp.example.com`, without a trailing slash even if
you configured `resourceURL` with one. With Keycloak, that is one audience
mapper on the agent's client with that value, instead of one per route.
Routes outside the list still need their own audience.

A leaked token for the Gateway audience reaches every listed route, so list
namespaces or routes, never everything (the chart refuses `*`). Policies see
`input.audience` (`gateway` or `route`) and can require a route's own
audience for sensitive servers. See
[Gateway audience](components/gateway/README.md#gateway-audience).
