# SPEC-0015 plan: Upstream token exchange

## Design

- **API (R01).** `api/v1alpha1`: `MCPServerTokenExchange{Audience string,
  Scopes []string}` and `MCPServerSpec.TokenExchange`. `validation.go`:
  audience required and an absolute URI, scopes are scope tokens, not with
  `credential`, only with `transport: http`. Both CRD copies gain the field
  (`audience` `maxLength: 2048`, scopes `maxItems: 20`) and CEL rules for
  the exclusions. DeepCopy copies it.
- **Route model.** `model.Route.TokenExchange` (not JSON) from the watcher;
  `/v1/servers` reports `tokenExchange: {audience}` so the console can say
  the server gets per-user tokens.
- **Issuer settings (R02).** `clientauth.Issuer` gains
  `TokenExchange *TokenExchangeClient` (`clientID`, `clientSecretKey`,
  optional `tokenEndpoint`). `--token-exchange-secret-dir` names the
  mounted Secret; `Config.Validate` refuses exchange settings without the
  directory and an http token endpoint without `allowHTTP`. Chart:
  `gateway.auth.issuers[].tokenExchange`, `gateway.auth.tokenExchangeSecret`
  (mounted read-only at `/etc/kavrynt/token-exchange`, like the upstream
  credentials Secret); schema updated.
- **Exchange (R03 to R05).** `internal/gateway/clientauth/exchange.go`:
  `Authenticator.Exchange(r *http.Request, identity Identity, te
  *v1alpha1.MCPServerTokenExchange) (string, error)`. It takes the subject
  token from the request's `Authorization` header (it is never stored in
  `Identity`), finds the issuer that verified it, reads the client secret
  file on a cache miss (rotation applies at once), discovers
  `token_endpoint` from the issuer metadata when not configured (with
  `jwks_uri` in the same fetch), and posts the RFC 8693 form with
  `client_secret_basic`. The same egress-guarded client as key fetches:
  5 s timeout, 1 MiB answers, no redirects, https unless `allowHTTP`.
  Answers need `access_token` and `token_type` `Bearer` (any case).
- **Cache (R04).** Key: SHA-256 of the subject token, audience, and sorted
  scopes. Entry lives until `expires_in - 30 s` (not cached when that is
  under one second or `expires_in` is missing). At most 10,000 entries;
  when full, expired entries go first, then the oldest.
- **Errors (R06, R07).** `ErrExchangeUnreachable` (dial, timeout) answers
  `503`; any other failure `502`. Fixed messages ("token exchange
  failed"), outcome `exchange_failed`, audit decision `unavailable`. With
  client authentication off, a route with `tokenExchange` answers `503`
  and the Gateway logs it once per route.
- **Never disclosed (R08).** Errors carry the issuer and HTTP status only;
  the token endpoint's error body is not read into messages. Tests search
  logs, metrics, traffic, audit, and responses for the tokens and secret.
- **Operator (R10).** `discover` skips servers with `tokenExchange`:
  `ToolsDiscovered=False`, reason `CallerTokenRequired`, no retry timer
  beyond the refresh interval.
- **E2E (R09).** The test issuer gains `POST /token`: checks HTTP Basic
  against `CLIENT_ID`/`CLIENT_SECRET`, verifies the subject token with its
  own key, and signs a new token with the requested `audience`, the same
  `sub`, and `act: {sub: <client_id>}`. In `e2e-kind.sh`, the
  authentication upgrade also sets the issuer's `tokenExchange` and the
  Secret; a `tickets-mcp-server` (the e2e image) is registered with
  `tokenExchange.audience: https://tickets.e2e.test`; `tools/list` works,
  and `/debug/headers` shows a bearer token whose claims have that audience
  and `sub: e2e-user` and that differs from the caller's.

## Affected files

| Path | Change |
| --- | --- |
| `api/v1alpha1/mcpserver_types.go`, `validation.go`, tests | Field and validation |
| `config/crd/bases/kavrynt.io_mcpservers.yaml`, chart CRD copy | Schema |
| `internal/gateway/model`, `kube/watcher.go` | Route field |
| `internal/gateway/clientauth/exchange.go`, `exchange_test.go`, `keys.go` | Exchange, cache, metadata |
| `internal/gateway/api/api.go`, `traffic.go`, tests | Proxy step, outcome |
| `internal/operator/controller/mcpserver_controller.go`, tests | Skip discovery |
| `cmd/gateway/main.go` | Flag |
| Gateway chart values, schema, Deployment, `charts/kavrynt/tests/clientauth_test.yaml` | Settings and mount |
| `test/e2e/mcp-server/issuer.go`, `scripts/e2e-kind.sh` | Token endpoint, e2e section |
| `examples/kavryctl/mcp-server-token-exchange.yaml`, docs, CLAUDE.md | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0015-R01 | `validation_test.go`; `charts/kavrynt/tests/crd_test.yaml` (both copies identical, rules present) |
| SPEC-0015-R02 | `clientauth` config tests; `charts/kavrynt/tests/clientauth_test.yaml` (mount, no Secret RBAC) |
| SPEC-0015-R03 | `exchange_test.go` (form, Basic auth, forwarded header) |
| SPEC-0015-R04 | `exchange_test.go` (one request for two calls, expiry, bound) |
| SPEC-0015-R05 | `exchange_test.go` (http refused, size and time limits, redirects) |
| SPEC-0015-R06, R07 | `api` tests (`502`, `503`, outcome, nothing upstream) |
| SPEC-0015-R08 | `api` and `exchange` tests searching every output |
| SPEC-0015-R09 | `scripts/e2e-kind.sh` |
| SPEC-0015-R10 | Operator controller test |

## Rollout

Additive. Release notes: configuring the issuer and Secret, the Operator's
`CallerTokenRequired` reason.

## Risks

- Authorization servers differ in RFC 8693 support (audience versus
  resource, required scopes). Mitigation: documented settings for one
  common server; errors name the issuer and HTTP status.
- The Gateway's client secret can exchange any token it receives. It is
  mounted, never logged, and rotated by updating the Secret.
