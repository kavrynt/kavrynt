# Gateway

Routes MCP requests to `MCPServer` endpoints. Source: `cmd/gateway`,
`internal/gateway`.

## Behaviour

- Watches `MCPServer` resources through a read-only informer cache
  (`internal/gateway/kube`). A server gets a route when the operator reports
  `Ready=True`, the spec passes validation, and the transport is `http`.
  A request for a registered server without a route answers `404` saying
  why: it uses the `stdio` transport, or it is not Ready. A `stdio` server
  can run behind a Streamable HTTP bridge in its own pod and be registered
  with the bridge's `http` endpoint
  ([example](../../../examples/stdio-bridge/filesystem.yaml)); `kavryctl
  validate` and `register` warn about `stdio` manifests.
- Route path: `/mcp/<namespace>.<name>`. Only `POST`, `GET`, and `DELETE`
  are forwarded, what MCP Streamable HTTP uses; other methods get `405`
  with `Allow: GET, POST, DELETE` (SPEC-0029). The query string is appended
  to the endpoint URL. A path below the route (`/mcp/<route>/<suffix>`)
  gets `404` unless the server sets `spec.forwardSubpaths: true`, which
  appends the suffix to the endpoint path, without `.` or `..` segments.
- Endpoints are restricted to absolute `http`/`https` URLs without credentials
  or fragments.
- Egress guard: the Gateway never connects to unspecified, link-local
  (including `169.254.169.254`), multicast, or known cloud metadata addresses
  (`fd00:ec2::254`, `100.100.100.200`), nor to loopback unless
  `--allow-loopback-endpoints` is set. The check runs on the resolved address
  at connect time, so DNS names and rebinding cannot bypass it. Refused
  requests return `502`.
- Upstream redirects are passed to the client, never followed.
- MCP Streamable HTTP checks (SPEC-0018), on every route, answered with
  `400` and outcome `protocol_error` before anything is forwarded (for a
  `POST`, as a JSON-RPC error with code `-32600` and the request's `id`,
  `null` for a batch): an
  `MCP-Protocol-Version` other than `2024-11-05`, `2025-03-26`,
  `2025-06-18`, `2025-11-25`, or `2026-07-28` (the answer lists them); a
  JSON-RPC batch (an array body); an `Mcp-Method` header that names another
  method than the body, or an `Mcp-Name` header on `tools/call` that names
  another tool than `params.name`; and, from `2026-07-28`, a request whose
  body has a `method` but no `Mcp-Method` header. Requests without
  `MCP-Protocol-Version` are treated as `2025-03-26`. `GET` event streams
  and `DELETE` session requests are not checked, and `Last-Event-ID` and
  `Mcp-Session-Id` are forwarded unchanged. Bodies over 1 MiB or not JSON
  are forwarded unchecked (unless a policy is on). `--strict-protocol=false`
  (chart: `config.strictProtocol`) forwards such requests while clients are
  migrated.
- Path suffixes with `.` or `..` segments are rejected with `400`, so callers
  cannot leave the endpoint's path.
- A request that already carries `X-Kavrynt-Route` (an endpoint pointing back
  at a Gateway) is rejected with `508` instead of looping.
- Browser protection: requests whose `Origin` is not loopback or listed in
  `--allowed-origins` are rejected with `403` (MCP DNS-rebinding guidance).
  Clients that are not browsers send no `Origin` and are unaffected.
- Proxied responses carry `Content-Security-Policy: sandbox` and
  `X-Content-Type-Options: nosniff`, so upstream content cannot run scripts
  on the Gateway origin, which the console shares.
- Upstream failures return a fixed message (`upstream request failed`, or
  `504` with `upstream request timed out`) that does not reveal the endpoint.
- `--request-timeout` bounds each request until its response is complete.
  `text/event-stream` responses are bounded only until their headers arrive,
  then flushed event by event for as long as the client stays connected.
- No token passthrough: `Authorization`, `Cookie`, and `Proxy-Authorization`
  are always removed before forwarding, along with hop-by-hop headers and
  headers named in `Connection`. Upstream `Set-Cookie` is dropped because
  every route shares the Gateway origin, and `Server` and `X-Powered-By`
  because they disclose the upstream's software. Extra headers can be stripped with
  `--strip-request-headers`.
- Upstream credentials: for a server that declares `spec.credential`, the
  Gateway sends the credential stored for it in the upstream credentials
  Secret (see below), replacing any header of that name from the client.
- Graceful shutdown: on `SIGTERM` the Gateway answers `/readyz` with `503`
  (`draining`), keeps serving for `--shutdown-delay` with every response
  closing its connection (`Connection: close`) so clients reconnect to
  another replica, then stops accepting
  connections and waits up to `--shutdown-timeout` for in-flight requests.
  A rolling restart of several replicas fails no MCP call (tested end to
  end).
- Bypass protection: with the chart's `networkPolicy.mcpServers` values,
  MCP server pods labelled `kavrynt.io/gateway-only: "true"` accept traffic
  only from the Gateway and the Operator, so clients cannot skip the
  Gateway; `networkPolicy.gateway` limits who can call the Gateway. Both
  need a CNI that enforces NetworkPolicy.
- MCP client authentication (on by default in the chart, see below): every
  `/mcp/` request needs a bearer token for its route.
- Tool authorization policy (opt-in, see below): every MCP request is
  allowed or denied by Rego policy before it is forwarded.
- Audit events (opt-in, see below): one JSON line per MCP request.
- Every `/mcp/` response carries `X-Request-Id`.
- Token exchange (see below): servers that declare `spec.tokenExchange` get
  the caller's token exchanged for one with their own audience.
- Session affinity (opt-in per server, see below): servers that declare
  `spec.sessionAffinity: MCPSession` get every request of an MCP session on
  the pod that started it.
- Not implemented: approvals. See ADR-0003.

## Upstream credentials

MCP servers that need their own API key or token declare it in the
`MCPServer` (SPEC-0011):

```yaml
spec:
  credential:
    header: Authorization   # default
    scheme: Bearer          # optional; the header value is "<scheme> <value>"
```

The platform team stores the value in one Secret in the Kavrynt namespace,
named by `global.upstreamCredentials.secretName`, under two keys per server:

| Key | Value |
| --- | --- |
| `<namespace>.<name>` | The credential |
| `<namespace>.<name>.origin` | The endpoint origin it may be sent to, `scheme://host:port` |

```bash
kubectl -n kavrynt-system create secret generic kavrynt-upstream-credentials \
  --from-literal=team-a.search="$SEARCH_TOKEN" \
  --from-literal=team-a.search.origin=http://search.team-a.svc.cluster.local:8080
```

The kubelet mounts the Secret into the Gateway and the Operator (`optional`,
so they start without it); neither has RBAC on Secrets. The credential is
used only when the pin matches the server's current endpoint (default ports
made explicit), so editing the `MCPServer` cannot send it to another host;
the pin also makes plain `http` an explicit choice. Changes apply without a
restart once the kubelet updates the mount. When the credential is missing,
empty, or not pinned to the endpoint, the Gateway answers `503 upstream
credential unavailable` without calling the server (outcome
`credential_unavailable`), and the Operator reports `ToolsDiscovered=False`
with reason `CredentialUnavailable` (naming the key) while `Ready` is
unchanged. `/v1/servers` shows `credential: {required, available}`; values
and pins never appear in responses, status, logs, metrics, or Kavrynt Cloud.
The CRD rejects credential headers that frame or route the request
(`Host`, `Content-Length`, `Connection`, ...), `Cookie`, and `Mcp-*`
headers, and `credential` on `stdio` servers. For a token on behalf of the
caller, use token exchange instead (below); a server cannot declare both.

## Audit events

With `--audit` (chart: `gateway.audit.enabled`, on in
`examples/helm/values-production.yaml`) the Gateway writes one event per
`/mcp/` request to standard output, as a single JSON line (SPEC-0013);
operational logs stay on standard error. Every request is recorded,
including those refused by authentication, policy, a missing upstream
credential, or an unknown route, when it completes.

| Field | Value |
| --- | --- |
| `type` | `kavrynt.audit.v1` |
| `time` | Request start, RFC 3339 with milliseconds, UTC |
| `instance`, `seq` | Gateway instance (pod name and a start nonce) and its event number, from 1 |
| `request_id` | Also returned to the client as `X-Request-Id` |
| `subject`, `client_id`, `issuer` | The verified caller; empty without client authentication |
| `route` | `namespace`, `name` |
| `http_method`, `mcp_method`, `tool` | What was asked |
| `decision` | `allow`, `deny` (policy), `unauthenticated`, `forbidden` (scope), `invalid` (bad request, unknown route), `unavailable` (issuer keys or upstream credential) |
| `reason` | Why it was refused |
| `policy_revision` | SHA-256 of the policy in force, with a policy |
| `outcome`, `status`, `duration_ms`, `bytes_in`, `bytes_out` | How it ended (traffic outcome) |
| `filtered_tools` | Tools removed from a `tools/list` answer by the policy (SPEC-0016); only present when some were |
| `prev_hash`, `hash` | The chain |

Fields that do not apply are present and empty, except `filtered_tools`.
`kavryctl audit verify` of a release before `filtered_tools` existed reports
events that carry it as breaks; verify with the `kavryctl` of the Gateway's
release. Events never contain tool
arguments, results, bodies, tokens, credentials, cookies, or headers; they do
identify people and agents, so treat them as personal data.

**Chain.** `hash` is the hex SHA-256 of `prev_hash` followed by the event's
canonical JSON without `hash` (fields in the order above, no whitespace);
`prev_hash` of an instance's first event is 64 zeros. `kavryctl audit verify
<file>` (or standard input) reads collected Gateway logs, skips other lines,
and reports each gap, reordering, or altered event. The chain is
tamper-evident, not tamper-proof: ship events out of the cluster with your
log pipeline (filter on `"type":"kavrynt.audit.v1"`).

**What verify proves.** A chain that verifies has every event of each
instance it shows, unaltered and in order, from `seq` 1 up to its last
event. It cannot show:

- events cut off the end of a chain: a log truncated after event N verifies
  like one that ended there;
- an instance whose events are all missing;
- a chain rewritten by someone who can write the log: the hashes use no
  key, so recomputing every hash after an edit gives a chain that verifies.

These are limits of a hash chain without an anchor outside the log. Keep a
copy where the Gateway's log writers cannot change it, soon after the
events are written: your log pipeline, or audit delivery to Kavrynt Cloud
(below). Then compare each instance's last `seq` in the copy with the
events you verify, and verify the copy, not only the cluster's logs.

**Back-pressure.** Up to `--audit-queue-size` (10,000) events wait to be
written. When the queue is full, `--audit-on-overflow=reject` (default)
answers new MCP requests `503` without forwarding them, so no call is served
without its record; `drop` serves them and counts the lost events. Metrics:
`kavrynt_gateway_audit_events_total`, `kavrynt_gateway_audit_overflow_total{action}`,
`kavrynt_gateway_audit_queue_length`. Queued events are written on graceful
shutdown.

**Delivery to Kavrynt Cloud** (SPEC-0017, opt-in). With `--audit-cloud-url`
(chart: `gateway.audit.cloud`) the Gateway also sends every written event to
Kavrynt Cloud, so the chains can be checked outside the cluster. Events
leave the cluster with the caller's `subject`, `client_id`, and `issuer`
(personal data); never arguments, results, or tokens. Standard output keeps
every event either way.

- `POST <url>/v1/sync/audit-events` with `Authorization: Bearer <cluster
  token>` and `{"events": [...]}`: at most 500 events and 1 MiB per batch,
  sent when a batch is full and at least every 10 seconds, in `seq` order.
  The kavrynt-cloud ingestion service must serve this endpoint; until it
  does, delivery fails and `KavryntAuditDeliveryStale` fires.
- Only an answer `2xx` removes events. Anything else retries the same events
  with exponential backoff and jitter (1 s up to 5 min), or `Retry-After` on
  `429` and `503`, so Cloud can receive an event twice; `instance` and `seq`
  identify it.
- Undelivered events wait in a buffer of `--audit-cloud-buffer-size`
  (100,000). When it is full the oldest are dropped from delivery and
  counted; MCP requests are never delayed by delivery.
- The token is read from `--audit-cloud-token-file` before every attempt
  (a rotated Secret needs no restart) and never logged. Requests use HTTPS
  only, follow no redirects, time out after 30 s, and refuse link-local,
  cloud metadata, and loopback addresses like MCP traffic.
- Metrics: `kavrynt_gateway_audit_delivery_total{result}` (`delivered`,
  `throttled`, `rejected`, `server_error`, `network`, `token`),
  `kavrynt_gateway_audit_delivery_buffer_length`,
  `kavrynt_gateway_audit_delivery_dropped_total`, and
  `kavrynt_gateway_audit_delivery_last_success_timestamp_seconds`.
- At shutdown, after writing its queue, the Gateway makes one last delivery
  attempt within `--shutdown-timeout`.

## Tool authorization policy

With `--policy-dir` (chart: `gateway.policy.enabled` and
`gateway.policy.configMap`) the Gateway evaluates a Rego policy, with Open
Policy Agent embedded, before forwarding every `/mcp/` request (SPEC-0012).
The policy is the `.rego` files of a ConfigMap, mounted by the kubelet. It
must define `data.kavrynt.authz.decision`; the request is forwarded only when
that is an object with `"allow": true`. An undefined decision, any other
value, an evaluation error, or an evaluation longer than 100 ms denies.

Input:

| Field | Value |
| --- | --- |
| `subject`, `client_id`, `issuer`, `scopes` | The verified caller (MCP client authentication); empty without it |
| `route.namespace`, `route.name` | The `MCPServer` |
| `http_method` | `POST`, `GET`, `DELETE` |
| `mcp_method` | JSON-RPC method of a `POST`; empty for other methods and for JSON-RPC responses |
| `tool` | `params.name` of `tools/call`; empty otherwise |
| `tool_annotations` | For `tools/call`, the behavior hints the Operator recorded for `tool` on this route (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`), absent hints left out; `{}` otherwise |
| `tool_discovered` | `true` when `tool` is among the route's tools and the discovery of the current spec succeeded (`ToolsDiscovered` is `True` for the `MCPServer`'s generation); `false` otherwise |
| `time` | Request time, RFC 3339, UTC |

Tool arguments are never part of the input. `tool_annotations` and
`tool_discovered` come only from the `MCPServer` status, never from the
request. **Hints are the MCP server's own claims:** a malicious or buggy
server can call a destructive tool read-only, so decide on them only for
servers whose code you trust. A hint the server did not report is absent,
not `false`; MCP's defaults for absent hints are `readOnlyHint: false`,
`destructiveHint: true`, `idempotentHint: false`, `openWorldHint: true`. The
policy sees hints only once a discovery of the current spec has succeeded:
after a failed discovery or a spec change, every tool has
`tool_annotations: {}` and `tool_discovered: false`, and so does a tool the
server added since the last discovery. A denied request gets `403` with
a JSON-RPC error (`code` -32001, `message` `denied by policy: <reason>`, the
policy's `reason` with control characters removed and at most 200
characters) and outcome `denied`. With a policy, a `POST` the Gateway cannot
read (not a single JSON object, over 1 MiB, `tools/call` without a string
`params.name`) or whose `Mcp-Method`/`Mcp-Name` headers disagree with the body
gets `400`. None of these reach the MCP server.

A policy that does not compile stops the Gateway at start, naming the file
and line. An update that does not compile is logged and counted in
`kavrynt_gateway_policy_load_failures_total`, and the previous policy stays in
force. `kavrynt_gateway_policy_info{revision}` names the active revision (the
SHA-256 of the files) on each replica, and
`kavrynt_gateway_policy_decisions_total{result}` counts decisions.

**How long a change takes.** A ConfigMap change is not in force at once.
The kubelet updates the mounted files after up to its sync period (one
minute by default) plus its cache delay, and the Gateway checks the files
every 10 seconds, so expect one to two minutes; about 66 seconds was
measured on Kind. Replicas switch independently. Until a replica switches,
it decides with the previous policy, so a call the new policy denies can
still succeed there: tightening a policy is not immediate. To check:

```bash
kubectl -n kavrynt-system logs deploy/kavrynt-gateway | grep 'policy loaded'
```

or compare `kavrynt_gateway_policy_info{revision}` across replicas with
`sha256sum` of the policy files; each audit event's `policy_revision` names
the policy that decided it. To apply a change at once, for example during
an incident, restart the Gateway after editing the ConfigMap:
`kubectl -n kavrynt-system rollout restart deploy/kavrynt-gateway`. New pods
mount the current ConfigMap and load it before they are ready. Policies
cannot reach the network (`http.send`, `net.lookup_ip_addr`) or the Gateway's
environment (`opa.runtime`). Whoever can edit the ConfigMap controls
authorization: restrict it with RBAC. Examples, tested in CI:
[`examples/policy`](../../examples/policy) (allow authenticated callers, a
per-client tool allow-list, read-only methods, and read-only tools of
trusted servers by their reported hints).

**Filtered tool lists.** With `--policy-filter-tools-list` (chart:
`gateway.policy.filterToolsList`, off by default) a `tools/list` answer keeps
only the tools the policy would let the same caller call (SPEC-0016): for
each tool the policy is asked with `mcp_method: tools/call`, `tool: <name>`,
that tool's recorded `tool_annotations` and `tool_discovered` (not the
annotations in the answer), and the caller's other input. Every other field of the answer (`nextCursor`,
`_meta`, the kept tools as sent) is unchanged; in an event stream only the
response event is changed. The Gateway asks the server for an uncompressed
answer. If the evaluations take longer than 500 ms together, the answer is
over 1 MiB, or it is not a `tools/list` result, the caller gets an empty
`tools` list and the reason is logged. `kavrynt_gateway_policy_filtered_tools_total{route}`
counts removed tools, and the audit event has `filtered_tools`. A policy
that denies every `tools/call` lists no tools. Answers are filtered afresh
each time, so a policy update applies to the next list.

## MCP client authentication

With `--auth` (chart: `gateway.auth.enabled`, on by default; SPEC-0014) the
Gateway is an OAuth 2.1 resource server (SPEC-0006). The chart refuses to
render without `gateway.auth.resourceURL` and `gateway.auth.issuers` unless
`gateway.auth.enabled=false` is set, in which case every route is open and
the Gateway logs a warning at start. It never issues tokens: agents get them from
your authorization server, which the Gateway trusts by configuration.

A request to `/mcp/<namespace>.<name>` is forwarded only with
`Authorization: Bearer <token>` where the token is a JWT that:

- is signed with an asymmetric algorithm (RS, PS, ES 256/384/512 or EdDSA;
  never `none` or HMAC) by a key of the issuer's key set;
- has `iss` exactly equal to a trusted issuer;
- has `exp` in the future and, if present, `nbf` and `iat` not in the future
  (60 s leeway);
- has an `aud` that contains the route's resource URI,
  `<resourceURL>/mcp/<namespace>.<name>` (RFC 8707): a token for another
  route, for the Gateway's base URL, or for any other resource is refused;
- carries every scope in `--auth-required-scopes` (`scope`, space-separated,
  or `scp`).

Answers:

| Case | Status | `WWW-Authenticate` |
| --- | --- | --- |
| No bearer token | `401` | `Bearer [scope="..."], resource_metadata="<url>"` |
| Token in a query parameter or form field | `400` | `error="invalid_request"` |
| Invalid, expired, wrong issuer or audience | `401` | `error="invalid_token"` (the body never says which check failed) |
| Missing required scope | `403` | `error="insufficient_scope", scope="..."` |
| Issuer's keys cannot be loaded | `503`, `Retry-After: 60` | |

The check runs before the route is looked up, so callers without a valid
token cannot tell registered routes from others. The token is removed before
forwarding (no passthrough) and never logged, recorded, or returned.

**Discovery.** `GET /.well-known/oauth-protected-resource/mcp/<route>`
(no authentication) serves RFC 9728 metadata: `resource`,
`authorization_servers` (the trusted issuers), `bearer_methods_supported`
(`header`), and `scopes_supported`. Standard MCP clients follow the
`resource_metadata` URL of the `401` to find the authorization server.

**Keys.** Each issuer's key set comes from its `jwksURI`, or from its
metadata (RFC 8414 `/.well-known/oauth-authorization-server`, then OpenID
Connect discovery), whose `issuer` must match. Keys are cached, refreshed
every 15 minutes, and fetched again for an unknown `kid` at most once a
minute. Requests use https (unless the issuer sets `allowHTTP`, logged as a
warning), time out after 5 s, read at most 1 MiB, follow no redirects, and
pass the egress guard. If an issuer's keys cannot be loaded, only that
issuer's tokens fail (`503`); `/readyz` is not affected.

**Your authorization server** must issue JWT access tokens whose `aud` is
the route's resource URI. Authorization servers that support RFC 8707
resource indicators do this from the client's `resource` parameter; others
need an audience configured per route. Opaque tokens and token introspection
are not supported. [Setting up MCP client authentication](../../AUTH-SETUP.md)
walks through Keycloak.

**Effects elsewhere.** `/v1/routes` lists names, versions, and transports
only, without endpoints. `GET /v1/session` reports `mcpAuth`. The console's
Playground offers a field for an access token, kept in page memory only and
sent only in the `Authorization` header of its own calls, and explains `401`,
`403`, and `503` answers. The console login never authorizes `/mcp/` calls.

## Token exchange

An `MCPServer` that needs a token on behalf of the caller declares
`spec.tokenExchange` (SPEC-0015):

```yaml
spec:
  transport: http
  endpoint: http://tickets.team-a.svc.cluster.local:8080/mcp
  tokenExchange:
    audience: https://tickets.example.com   # required, an absolute URI
    scopes: [tickets:read]                  # optional
```

For each call the Gateway exchanges the caller's verified access token at
the caller's authorization server (OAuth 2.0 Token Exchange, RFC 8693:
`subject_token` the caller's token, `subject_token_type` and
`requested_token_type` `urn:ietf:params:oauth:token-type:access_token`,
`audience` and `scope` from the spec) and sends only the returned token as
`Authorization: Bearer`. The server learns the user from the token; the
caller's own token never leaves the Gateway.

The Gateway is an OAuth client of each issuer that allows exchange, set on
the trusted issuer:

```yaml
gateway:
  auth:
    issuers:
      - issuer: https://login.example.com/realms/agents
        tokenExchange:
          clientID: kavrynt-gateway
          clientSecretKey: client-secret   # key in tokenExchangeSecret
          tokenEndpoint: ""                # optional; else token_endpoint from the issuer metadata
    tokenExchangeSecret: kavrynt-token-exchange
```

It authenticates with `client_secret_basic`. The client secret is read from
the Secret mounted by the kubelet (the Gateway has no RBAC on Secrets) at
each exchange, so rotating the Secret applies once the kubelet updates the
mount. Exchanged tokens are cached in memory per caller token (by SHA-256),
audience, and scopes until 30 seconds before `expires_in` (at most 10,000;
not cached without `expires_in`). Token endpoint requests use https unless
the issuer sets `allowHTTP`, time out after 5 s, read at most 1 MiB, follow
no redirects, and pass the egress guard. Your authorization server decides
which audiences the Gateway may ask for, and may add the acting client as
`act`.

The Gateway uses one OAuth client for every route, so the authorization
server cannot tell which route asks for an audience. The platform team
therefore lists the audiences each namespace's servers may request, in
`global.tokenExchangeAudiences` (`--token-exchange-audiences`, a JSON
object; `"*"` applies to every namespace):

```yaml
global:
  tokenExchangeAudiences:
    team-a: [https://tickets.example.com]
```

A server whose audience is not listed for its namespace is not `Ready`
(`AudienceNotAllowed`), so the Gateway removes its route and its calls get
`404` once the Gateway has synced. Until then, and whenever the Gateway's
own list does not allow the audience, the Gateway exchanges no token: the
call gets `503` (`exchange_failed`) without a request to the authorization
server. With the default, an empty list, no audience is allowed.

| Case | Status | Outcome |
| --- | --- | --- |
| The authorization server refuses, or answers without a bearer `access_token` | `502 token exchange failed` | `exchange_failed` |
| The token endpoint cannot be reached or times out | `503 token exchange unavailable` | `exchange_failed` |
| MCP client authentication is off | `503` (logged once per route) | `exchange_failed` |

None of these reach the server. Caller tokens, exchanged tokens, and the
client secret never appear in logs, metrics, traffic, audit events, status,
or responses. The Operator does not list the tools of such a server (it has
no caller): `ToolsDiscovered=False` with reason `CallerTokenRequired`, and
`Ready` is unaffected. `/v1/servers` shows `tokenExchange: {audience}`.

## Session affinity

MCP Streamable HTTP lets a server start a session in its `initialize`
answer (`Mcp-Session-Id`), and servers built with the official SDKs keep
that session in memory. Behind a Service with several replicas, the
requests of one session then land on different pods, which answer `404`
(`session_not_found`). `sessionAffinity: ClientIP` on the Service does not
help, since every request comes from a Gateway pod. Declare
`spec.sessionAffinity: MCPSession` (SPEC-0026):

```yaml
spec:
  transport: http
  endpoint: http://search.team-a.svc.cluster.local:8080/mcp
  sessionAffinity: MCPSession
```

- The endpoint must be `http` and name a Service by its cluster DNS name
  (`<service>.<namespace>.svc[.<cluster-domain>]`); otherwise the server is
  not `Accepted` (`InvalidSpec`). The Gateway connects to a pod's address,
  so a certificate for the Service name would not verify; use a mesh for
  TLS between the Gateway and pods.
- A request without `Mcp-Session-Id` goes to the next ready pod of the
  Service, from its EndpointSlices in the namespaces the Gateway watches,
  with the Service host in `Host`. When the answer starts a session, the
  client gets `<tag>.<session ID>`, where the tag is derived from the pod's
  UID and names neither its address nor its name. Every later request of
  the session (`POST`, the `GET` stream, `DELETE`) goes to that pod with
  the server's own session ID, from any Gateway replica: the replicas share
  nothing.
- A session whose pod is no longer ready, or a session ID the Gateway did
  not issue, gets `404` without being forwarded, so the client starts a new
  session. A session is not moved to another pod.
- New sessions with no ready pod, or a Service in a namespace the Gateway
  does not watch, get `503`. Pods that are not ready, not serving, or
  terminating are not used. Pod addresses pass the same egress checks as
  any endpoint.
- The Service must have one port. EndpointSlices list target ports, so with
  several the Gateway cannot tell which one the endpoint's Service port
  maps to without reading the Service; it uses none, and new sessions get
  `503`.
- The Operator lists such a server's tools from one ready pod.
- The Gateway reads EndpointSlices (`get`, `list`, `watch`) in the
  namespaces it watches and keeps them in its cache; the Operator may
  `list` them.

Without session affinity, a server that keeps session state needs one
replica. Its lost sessions are counted as `session_not_found`, and the
console's server page says so.

## High availability

The Gateway keeps no state that replicas must share: routes come from the
Kubernetes API, and console sessions are signed with the key in the mounted
admin Secret, so every replica accepts them. Run several replicas with
`gateway.replicaCount` or `gateway.autoscaling`; the chart then adds a
PodDisruptionBudget and soft spread across nodes and zones.
`examples/helm/values-production.yaml` is the tested profile.

Per replica, by design: recent traffic (`/v1/traffic`, which names the
replica in `instance`), the traffic conditions of the console alerts
(`/v1/alerts`), and the console login limiter (five failed logins a
minute per client address, per replica). Use `/metrics` in Prometheus for the
cluster-wide view.

## Endpoints

| Path | Purpose |
| --- | --- |
| `GET /healthz` | Liveness |
| `GET /readyz` | `200` after the `MCPServer` cache has synced once; `503` while draining on shutdown |
| `GET /version` | Build metadata |
| `GET /metrics` | Prometheus metrics: MCP traffic (below), Gateway counters, Go runtime, and process |
| `GET /v1/routes` | Current route table: name, version, transport, and the endpoint without credentials, query, or fragment (no endpoint while MCP client authentication is on) |
| `GET /v1/servers` | Every watched `MCPServer`: conditions, discovered tools, the `serverInfo` it reported, whether it is routed. Leaves out `command`, `args`, `environment`, and the endpoint query. Needs a console session when console login is on |
| `GET /v1/traffic` | `instance` (this replica's name) and `requests`: the last 500 proxied requests on this replica, newest first: route, method, tool, outcome, status, latency, sizes. Needs a console session when console login is on |
| `GET /v1/alerts` | `instance`, `evaluatedAt`, and `alerts`: the [console alerts](#console-alerts) that are active, then those resolved in the last hour: `name`, `severity`, `scope` (`route`, optional `tool`), `summary`, `since`, `resolvedAt`, `runbookURL`. Needs a console session when console login is on |
| `GET /v1/history?range=1h\|6h\|24h[&route=<route>[&tool=<tool>]]` | The [dashboards'](#console-dashboards) history of this replica for all routes, a route, or a tool: `instance`, `scope`, `range`, `stepSeconds`, `start`, `end`, `since`, `points` (`time`, `ok`, `toolError`, `upstreamError`, `other`, `errorRate`, `p50Ms`, `p95Ms`, and outside a tool `bytesIn`, `bytesOut`), `routes` and `tools` totals (`calls`, `errors`, `errorRate`, `p95Ms`), the console `alerts` active in the range, and `grafanaURL`. `400` for another `range`, a route that is not routed, a tool without a route, or a malformed tool name. Needs a console session when console login is on |
| `GET`, `POST`, `DELETE /v1/session` | Console login: report the session (or `{"enabled":false}`) and `mcpAuth`, sign in with `{"username":"admin","password":"..."}` as JSON, sign out |
| `GET /.well-known/oauth-protected-resource/mcp/<route>` | Protected Resource Metadata of a route (RFC 9728), only with MCP client authentication |
| `GET /ui/` | Web console (`--console`); `/` redirects here |
| `* /mcp/<route>/...` | Proxied MCP traffic; with MCP client authentication, only with a bearer token for the route |

## Console

A read-only web console served by the Gateway itself, enabled by default
(chart: `console.enabled`). It needs no extra RBAC and no external assets;
everything it shows comes from `/v1/servers`, `/v1/traffic`, `/v1/alerts`,
`/v1/history`, `/readyz`, and `/version`.

- **Overview** (the landing page): Gateway readiness and version, counts of
  servers, routed servers, and tools, and recent request volume, error rate,
  and p95 latency. **Needs attention** lists servers that are not routed,
  whose tool discovery failed, or whose recent requests mostly fail (at
  least half of at least five), with the reason, and links to the Alerts
  page. **Failing tools** lists the five tools with the most failed calls.
- **Dashboard**: charts of the last hour, 6 hours, or 24 hours for all
  servers, one server, or one tool, with the busiest and most failing
  servers and tools (see [Console dashboards](#console-dashboards)).
- **Servers**: every `MCPServer` with its health, recent request count and
  error rate, declared and reported version (marked when they differ),
  conditions, and discovered tools with input schemas. Its **Dashboard** tab
  charts the server and lists each tool's calls, errors, error rate, and
  p95 for the range; choosing a tool charts that tool. A connect
  panel gives the route URL and ready-to-paste MCP client, Python agent, and
  curl snippets. **Copy diagnostics** copies a metadata-only summary
  (conditions, tools, recent traffic; no tool inputs or results, and the
  endpoint's query string redacted) for an issue or a chat.
- **Tools**: every discovered tool across all servers, searchable by name,
  description, and route, with its reported behavior hints and a link to
  try it in the Playground. A server's Tools tab shows the same hints.
- **Playground**: opens an MCP session (`initialize`, `tools/list`) through
  the Gateway route, builds a form from each tool's input schema, and calls
  tools (`tools/call`) or any raw JSON-RPC method. JSON and event-stream
  responses are supported. Calls and results stay in the page's memory.
- **Traffic**: recent requests refreshed every two seconds, filtered by
  route, method, tool, and outcome, with per-tool calls, error rate, and
  p50/p95 latency, 100 rows at a time. **What do the outcomes mean?**
  explains each outcome (see below).
- **Alerts**: the [console alerts](#console-alerts) that are active, critical
  before warning and oldest first, then those resolved in the last hour,
  each with when it started, a link to its server or tool, and its runbook.
  The navigation shows the number of active alerts on every page.

Filters are part of the URL, so a filtered view can be shared as a link.
Polling stops while the browser tab is hidden. Traffic covers only the
Gateway replica that served the page (named above the table) and its last 500
requests; use `/metrics` for history.

```bash
kubectl -n kavrynt-system port-forward service/kavrynt-gateway 18080:8080
# open http://localhost:18080/ui/ and sign in as admin with:
kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d
```

### Console dashboards

Each Gateway replica keeps its own calls of the last 24 hours in memory: per
route, calls by outcome class, latency in the buckets of
`kavrynt_gateway_mcp_request_duration_seconds`, and bytes in and out; per
tool, `tools/call` requests by outcome class and latency. Points are one
minute apart for the last hour and five minutes apart for 6 and 24 hours.
The classes are `ok`, `toolError` (`tool_error`, `rpc_error`),
`upstreamError` (`upstream_error`, `upstream_unreachable`, `timeout`), and
`other` (every other outcome, such as calls the Gateway refused). The error
rate is the share of calls that are not `ok`; p50 and p95 are computed like
`histogram_quantile`.

The Dashboard page and each server's Dashboard tab chart requests per
minute by class, error rate, p50 and p95 latency, and bytes per second
(not for a tool: bytes are counted per route, as in the metrics). The
console alerts that started or resolved in the range are marked with
dashed lines and listed under the charts; resolved alerts are kept for 24
hours for this, while `/v1/alerts` lists only the last hour. Hovering a
chart, or focusing it and using the arrow keys, shows its values; a table
of the same numbers is there for screen readers. The range and scope are
part of the URL.

The numbers are this replica's since it started: a restart begins the
history again, and replicas do not add up their calls. For cluster-wide
or longer history, use the Grafana dashboards (chart:
`metrics.dashboard.enabled`), which the page links when
`console.grafanaURL` (`--grafana-url`) is set.

Memory is fixed by the number of routes and tools: about 29 KiB per route
and 23 KiB per tool. Tools are charted on their own up to
`console.history.maxTools` (`--history-max-tools`, default 500) across
routes; calls to further tools count as their route's `other` tool. With
the default and 300 routes the history takes about 27 MiB. Routes of
deleted `MCPServer`s are dropped within a minute.

### Console alerts

The Gateway evaluates five conditions every 15 seconds, with the same
thresholds as the chart's Prometheus alerts, so teams without Prometheus see
the same problems. The umbrella chart writes the thresholds from
`metrics.prometheusRule` into the ConfigMap `<release>-console-alerts`, which
the Gateway mounts and rereads when it changes (`--alerts-config`), whether
or not the rules are installed.

| Condition | Holds when | For | Runbook |
| --- | --- | --- | --- |
| `ServerNotReady` | The `MCPServer`'s `Ready` is not `True` | `notReadyFor` (10m) | `KavryntMCPServerNotReady` |
| `DiscoveryFailing` | A `Ready` server's `ToolsDiscovered` is `False` with `DiscoveryFailed` or `CredentialUnavailable` | `discoveryFailingFor` (15m) | `KavryntMCPToolDiscoveryFailing` |
| `UpstreamErrors` | Over `errorRatio` (5%) of a route's calls in the last 10 minutes ended in `upstream_error`, `upstream_unreachable`, or `timeout` | 5m | `KavryntMCPUpstreamErrorsHigh` |
| `ToolErrors` | Over `toolErrorRatio` (20%) of a tool's `tools/call` requests in the last 10 minutes ended in `tool_error` or `rpc_error` | 10m | `KavryntMCPToolErrorsHigh` |
| `SlowRoute` | A route's p95 latency in the last 10 minutes is over `latencyP95Seconds` (5s) | 15m | `KavryntMCPLatencyHigh` |

Route conditions need `minRequestsPerSecond` (0.05, 30 calls in 10 minutes),
tool conditions `minToolRequestsPerSecond` (0.02, 12 calls). Unlike
`KavryntMCPLatencyHigh`, `SlowRoute` also needs that minimum. All are
`warning`. Status conditions start at their condition's
`lastTransitionTime`, so every replica reports them alike. Traffic
conditions count this replica's own calls; Prometheus sums every replica.
Resolved alerts are kept for an hour in memory; alerts about a deleted
`MCPServer` are removed. They carry route and tool names and counts only.

### Console login

The console has one user, `admin`, like Argo CD's initial admin account. The
chart generates a random 32-character password and a session signing key
into the Secret `<release>-gateway-console-admin` (`kavrynt-gateway-console-admin`
for the default release), keeps them across `helm upgrade`, and keeps the
Secret on `helm uninstall`. The kubelet mounts the Secret into the Gateway;
the Gateway's service account has no RBAC on Secrets.

- Sessions are `HttpOnly`, `SameSite=Strict` cookies scoped to `/v1/` and
  last 12 hours (`console.auth.sessionTTL`). They are `Secure` when the
  request arrives over HTTPS or with `X-Forwarded-Proto: https`.
- To change the password, update the Secret's `password` key. Every session
  ends once the kubelet updates the mount (about a minute); no restart is
  needed.
- After five failed logins from one client within a minute, logins from
  that client get `429` until the minute has passed. The client is the
  connection's address. Behind an Ingress or load balancer that is the
  proxy, so the limit applies to everyone, unless the proxy's CIDRs are in
  `gateway.console.auth.trustedProxies` (`--console-trusted-proxies`): for
  requests from them, the client is the right-most `X-Forwarded-For`
  address that is not a trusted proxy (SPEC-0030). A header with no such
  address counts against the proxy, and `X-Forwarded-For` from other
  addresses is ignored. List only the proxies: an address listed there can
  name any client. Each trusted proxy must overwrite `X-Forwarded-For`, or
  append the address of the peer it received the request from; a proxy
  that passes on a client's header unchanged lets that client pick a new
  address for every attempt.
- The limiter tracks at most 10,000 clients per replica. When it is full it
  forgets clients whose minute has passed, then the oldest one that is not
  refused, never a refused one; with only refused clients left, further
  clients share one limit until room frees up.
- Failed and successful logins are logged with the client address. The
  password and session tokens are never logged.
- GitOps and `helm template` renders cannot look up the existing Secret and
  would generate a new password on every render. Create your own Secret with
  `password` and `sessionKey` keys and set `console.auth.existingSecret`.
- `console.auth.enabled=false` (flag `--console-auth=false`) turns the login
  off; `/v1/servers` and `/v1/traffic` are then open as before.

**What the login does not protect.** It covers the console and the two
endpoints it reads. It is not client authentication: unless MCP client
authentication is on, `/mcp/` routes stay open, so anyone who can reach the
Gateway can call every routed tool, with or without the console.
`/v1/routes`, `/healthz`, `/readyz`, `/version`, and `/metrics` stay open
too. When the console is opened from an address other than loopback, it
shows a banner saying whether MCP routes require access tokens.

To serve the console through an Ingress, add the Ingress origin to
`config.allowedOrigins`; the Gateway rejects logins and MCP calls from other
browser origins. Server and tool metadata is untrusted and is only rendered
as text; the console's `Content-Security-Policy` allows only its own scripts
and the Gateway's endpoints.

## MCP traffic metrics

Recorded per proxied request, metadata only (kavrynt-cloud LLD-0002). Bodies,
tool arguments, and results are never recorded.

| Metric | Labels |
| --- | --- |
| `kavrynt_gateway_mcp_requests_total` | `route`, `method`, `tool`, `outcome` |
| `kavrynt_gateway_mcp_request_duration_seconds` (histogram) | `route`, `method`, `tool`, `outcome` |
| `kavrynt_gateway_mcp_bytes_total` | `route`, `direction` (`in`, `out`) |

- `method` comes from the `Mcp-Method` header, else the JSON-RPC `method` in
  the first 1 MiB of a POST body (`unknown` when absent or larger). Non-POST
  requests use `http_<verb>`, for example `http_get` for streams.
- `tool` comes from `Mcp-Name`, else `params.name` of `tools/call`; `none`
  otherwise. Names longer than 128 characters or outside `[A-Za-z0-9_./-]`
  become `invalid`; after 200 distinct tools per route (50 methods), new
  values become `other`.
- `outcome`: `ok`, `tool_error` (`result.isError`), `rpc_error` (JSON-RPC
  `error`), `client_error` (4xx), `upstream_error` (5xx),
  `upstream_unreachable`, `timeout`, `incomplete` (response copy failed),
  `gateway_error` (unsupported transport or bad endpoint), or, with MCP
  client authentication on, `unauthenticated` (no valid token, `401`/`400`),
  `forbidden` (missing scope, `403`), and `auth_unavailable` (issuer keys
  unavailable, `503`); `credential_unavailable` (`503`) when a server's
  stored upstream credential is missing or not pinned to its endpoint;
  `denied` (`403`) when the tool authorization policy refuses the call;
  `protocol_error` (`400`) when the request is not well-formed MCP
  Streamable HTTP; `exchange_failed` (`502`/`503`) when the caller's
  token could not be exchanged for the server; and `session_not_found`
  (`404`) when a request with `Mcp-Session-Id` names a session the server
  does not know or, with session affinity, a pod that is no longer ready
  (it was `client_error` before `session_not_found` existed). JSON responses up
  to 1 MiB are inspected, and so are event-stream answers to a `POST`, event
  by event (events up to 1 MiB), until the one that answers the request.
  The answers the MCP specification gives for a feature a server does not
  offer count as `ok`: `405` to the optional `GET` event stream or to a
  `DELETE` that ends a session, and JSON-RPC "method not found"
  (`-32601`) to the `server/discover` probe.

The chart adds `prometheus.io/*` scrape annotations by default
(`gateway.metrics.scrapeAnnotations`). Optional: a Prometheus Operator
`ServiceMonitor` (`gateway.metrics.serviceMonitor.enabled`), the
"Kavrynt MCP Traffic" and "Kavrynt MCP Server" Grafana dashboards and your
own as sidecar-labelled ConfigMaps (`gateway.metrics.dashboard.*`), and
alerting rules with runbooks (`metrics.prometheusRule.enabled`; see
[docs/runbooks](../../runbooks/README.md)).

## Metrics reference

Every metric the Gateway exports on `/metrics`, besides the Go runtime
(`go_*`) and process (`process_*`) metrics. Labels are bounded: no metric
carries a caller identity, tool arguments, or results. The Operator's metrics
are in the [Operator reference](../operator/README.md#metrics-reference).

| Metric | Type | Labels and bounds |
| --- | --- | --- |
| `kavrynt_gateway_mcp_requests_total` | counter | `route` (one per routed `MCPServer`, `<namespace>.<name>`), `method` (50 per route, then `other`), `tool` (200 per route, then `other`; `none`, `invalid`), `outcome` (the list above) |
| `kavrynt_gateway_mcp_request_duration_seconds` | histogram | as `kavrynt_gateway_mcp_requests_total`; buckets 5 ms to 30 s |
| `kavrynt_gateway_mcp_bytes_total` | counter | `route`, `direction` (`in`, `out`) |
| `kavrynt_gateway_requests_total` | counter | none; every HTTP request the Gateway received |
| `kavrynt_gateway_proxied_requests_total` | counter | none; requests proxied to an MCP server to completion |
| `kavrynt_gateway_stripped_credential_requests_total` | counter | none; proxied requests whose `Authorization`, `Cookie`, or `Proxy-Authorization` was removed |
| `kavrynt_gateway_routes` | gauge | none; routes served now |
| `kavrynt_gateway_route_sync_success_total` | counter | none |
| `kavrynt_gateway_route_sync_failure_total` | counter | none |
| `kavrynt_gateway_audit_events_total` | counter | none; with audit on |
| `kavrynt_gateway_audit_overflow_total` | counter | `action` (`reject`, `drop`); with audit on |
| `kavrynt_gateway_audit_queue_length` | gauge | none; with audit on |
| `kavrynt_gateway_audit_delivery_total` | counter | `result` (`delivered`, `throttled`, `rejected`, `server_error`, `network`, `token`); with audit delivery to Kavrynt Cloud |
| `kavrynt_gateway_audit_delivery_buffer_length` | gauge | none; with audit delivery |
| `kavrynt_gateway_audit_delivery_dropped_total` | counter | none; with audit delivery |
| `kavrynt_gateway_audit_delivery_last_success_timestamp_seconds` | gauge | none; Unix time, the Gateway's start until the first accepted batch; with audit delivery |
| `kavrynt_gateway_policy_decisions_total` | counter | `result` (`allow`, `deny`, `error`); with a policy |
| `kavrynt_gateway_policy_load_failures_total` | counter | none; with a policy |
| `kavrynt_gateway_policy_info` | gauge | `revision` (the active policy only, value 1); with a policy |
| `kavrynt_gateway_policy_filtered_tools_total` | counter | `route`; with a policy |

Each Gateway replica exports its own series; sum over `instance` for the
whole Gateway. Example PromQL for custom panels:

```promql
# Error rate per MCP server
sum by (route) (rate(kavrynt_gateway_mcp_requests_total{outcome!="ok"}[5m]))
  / sum by (route) (rate(kavrynt_gateway_mcp_requests_total[5m]))

# p95 latency per tool of one server
histogram_quantile(0.95, sum by (tool, le) (rate(
  kavrynt_gateway_mcp_request_duration_seconds_bucket{route="team-a.search", method="tools/call"}[5m])))

# Tool calls per minute, by tool
sum by (route, tool) (rate(kavrynt_gateway_mcp_requests_total{method="tools/call"}[5m])) * 60
```

Calls per client are not in metrics, which carry no identity. Count them
from the audit events (`gateway.audit`), for example in your log pipeline
grouped by `client_id` or `subject`.

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--addr` | `:8080` | Listen address |
| `--watch-namespaces` | all (chart: `global.watchNamespaces`) | Comma-separated namespaces (`KAVRYNT_WATCH_NAMESPACES`) |
| `--strip-request-headers` | none | Extra headers never forwarded (`KAVRYNT_STRIP_REQUEST_HEADERS`) |
| `--request-timeout` | `30s` | Upstream request timeout; event streams only until response headers |
| `--allowed-origins` | none (chart: `config.allowedOrigins`) | Comma-separated browser origins accepted besides loopback (`KAVRYNT_ALLOWED_ORIGINS`) |
| `--console` | `true` (chart: `console.enabled`) | Serve the web console at `/ui/` |
| `--console-auth` | `true` (chart: `console.auth.enabled`) | Require the console admin login for the console, `/v1/servers`, `/v1/traffic`, and `/v1/alerts` (with `--console`). MCP routes are not affected |
| `--console-auth-dir` | none (chart: the mounted admin Secret) | Directory with the `password` and `sessionKey` files (`KAVRYNT_CONSOLE_AUTH_DIR`). Required with `--console-auth` |
| `--console-session-ttl` | `12h` (chart: `console.auth.sessionTTL`) | Console session lifetime |
| `--token-exchange-audiences` | none allowed (chart: `global.tokenExchangeAudiences`) | JSON object from namespace (or `"*"`) to the audiences tokens may be exchanged for (`KAVRYNT_TOKEN_EXCHANGE_AUDIENCES`) |
| `--console-trusted-proxies` | empty (chart: `console.auth.trustedProxies`) | Comma-separated CIDRs of proxies whose `X-Forwarded-For` names the client for the console login limit |
| `--allow-loopback-endpoints` | `false` | Allow endpoints that resolve to loopback; for running the Gateway on a workstation only |
| `--auth` | `false` (chart: `auth.enabled`, default `true`) | Require a bearer access token for the route on every `/mcp/` request |
| `--auth-resource-url` | none (chart: `auth.resourceURL`) | The Gateway's external https origin; a route's token audience is `<url>/mcp/<route>` (`KAVRYNT_AUTH_RESOURCE_URL`) |
| `--auth-issuers` | none (chart: `auth.issuers`) | Trusted authorization servers as JSON: `[{"issuer":"...","jwksURI":"...","allowHTTP":false}]` (`KAVRYNT_AUTH_ISSUERS`) |
| `--token-exchange-secret-dir` | none (chart: the mounted `auth.tokenExchangeSecret`) | Directory of the Secret with the client secrets named by the issuers' `tokenExchange.clientSecretKey` (`KAVRYNT_TOKEN_EXCHANGE_SECRET_DIR`) |
| `--auth-required-scopes` | none (chart: `auth.requiredScopes`) | Comma-separated scopes every token must carry (`KAVRYNT_AUTH_REQUIRED_SCOPES`) |
| `--audit` | `false` (chart: `audit.enabled`) | Write one audit event per MCP request to standard output |
| `--audit-queue-size` | `10000` (chart: `audit.queueSize`) | Audit events that may wait to be written |
| `--audit-on-overflow` | `reject` (chart: `audit.onOverflow`) | With a full queue: `reject` new MCP requests (`503`) or `drop` their events |
| `--audit-cloud-url` | none (chart: `audit.cloud.url` with `audit.cloud.enabled`) | Kavrynt Cloud base URL (https) to deliver audit events to; needs `--audit` (`KAVRYNT_AUDIT_CLOUD_URL`) |
| `--audit-cloud-token-file` | `/etc/kavrynt/audit-cloud/token` (chart: the mounted `audit.cloud.tokenSecret`) | Cluster token file (`KAVRYNT_AUDIT_CLOUD_TOKEN_FILE`) |
| `--audit-cloud-buffer-size` | `100000` (chart: `audit.cloud.bufferSize`) | Undelivered events kept; the oldest are dropped when full |
| `--policy-dir` | none (chart: the mounted `gateway.policy.configMap`) | Directory of the tool authorization policy (`.rego` files); every MCP request is then decided by it (`KAVRYNT_POLICY_DIR`) |
| `--upstream-credentials-dir` | none (chart: the mounted `global.upstreamCredentials.secretName`) | Directory of the upstream credentials Secret (`KAVRYNT_UPSTREAM_CREDENTIALS_DIR`) |
| `--shutdown-delay` | `5s` (chart: `config.shutdownDelay`) | How long to keep serving after `/readyz` starts failing on shutdown |
| `--shutdown-timeout` | `10s` (chart: `config.shutdownTimeout`) | How long to wait for in-flight requests once the server stops accepting connections |
| `--strict-protocol` | `true` (chart: `config.strictProtocol`) | Refuse requests that are not well-formed MCP Streamable HTTP; `false` forwards them |
| `--instance` | `KAVRYNT_INSTANCE`, else the hostname (chart: the pod name) | Name of this replica in `/v1/traffic` and `/v1/alerts` |
| `--history-max-tools` | `500` (chart: `console.history.maxTools`) | Tools the console dashboards chart on their own, across routes (1 to 10000); further tools count as their route's `other` tool |
| `--grafana-url` | none (chart: `console.grafanaURL`) | Grafana base URL (http or https, no credentials, query, or fragment) the dashboards link for cluster-wide history (`KAVRYNT_GRAFANA_URL`) |
| `--alerts-config` | none: the defaults (chart: the mounted `<release>-console-alerts`) | JSON file with the console alert thresholds, reread when it changes (`KAVRYNT_ALERTS_CONFIG`) |

Kubernetes access comes from the in-cluster service account (or `KUBECONFIG`
when run locally). The chart grants only `get`, `list`, `watch` on
`mcpservers.kavrynt.io` and, for session affinity,
`endpointslices.discovery.k8s.io`.

## Run locally

```bash
# Uses your current kubeconfig context. The console login needs a directory
# with password and sessionKey files; turn it off for local work.
go run ./cmd/gateway --addr 127.0.0.1:8080 --console-auth=false
curl -fsS http://127.0.0.1:8080/v1/routes
# console: http://127.0.0.1:8080/ui/
```

MCP servers on your workstation (for example `http://127.0.0.1:3000/mcp`)
need `--allow-loopback-endpoints`; never set it in a cluster:

```bash
go run ./cmd/gateway --addr 127.0.0.1:8080 --console-auth=false --allow-loopback-endpoints
```
