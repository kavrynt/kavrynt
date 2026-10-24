# SPEC-0006 plan: MCP client authentication

## Design

- **Package `internal/gateway/clientauth`.** `Authenticator` built from
  `Config{ResourceURL, Issuers []Issuer{Issuer, JWKSURI, AllowHTTP},
  RequiredScopes}`. `Authenticate(r, route) (Identity, *Refusal)` returns
  the verified identity or a refusal carrying status, `WWW-Authenticate`
  value, and traffic outcome (R03–R06, R14). It never returns which check
  failed to the caller (R05); the reason is logged at debug level without
  the token.
- **Token checks (R08, R09).** `github.com/go-jose/go-jose/v4` parses the
  JWS with an explicit allow-list (`RS256`, `RS384`, `RS512`, `PS256`,
  `PS384`, `PS512`, `ES256`, `ES384`, `ES512`, `EdDSA`), so `none` and HMAC
  never parse. Claims are decoded into a struct; `iss` is matched exactly
  against configured issuers before key lookup; `aud` may be a string or
  an array and must contain `<resourceURL>/mcp/<route>`; `exp` required;
  60 s skew.
- **Keys (R10–R12, R20).** `keySet` per issuer: JWKS URI from config or
  discovered from `/.well-known/oauth-authorization-server` then
  `/.well-known/openid-configuration` (issuer path rules of RFC 8414).
  Fetched with an `http.Client` on `egress.Transport(false)`, 5 s timeout,
  1 MiB body limit, HTTPS only unless `AllowHTTP`. Cached; refreshed every
  15 min; on unknown `kid`, refetched at most once a minute. Load failure
  → refusal `503` with `Retry-After: 60` and outcome `auth_unavailable`
  for that issuer only. `/readyz` does not consult issuers.
- **Metadata (R07).** `GET /.well-known/oauth-protected-resource/mcp/<route>`
  served by the handler before the `/mcp/` branch; `404` for unknown
  routes; JSON per RFC 9728.
- **Handler wiring.** `api.Handler.EnableClientAuth(a)`. The `/mcp/` path
  rejects tokens in the query string or form body (R04) before routing,
  authenticates, then strips credential headers as today (R13). The
  identity goes into the request context for SPEC-0012 and SPEC-0013.
- **Route listing (R18, R19).** `listRoutes` sanitises endpoints with
  `url.URL` (drop `User`, `RawQuery`, `Fragment`) always, and omits
  `endpoint` when client auth is enabled.
- **Console (R15–R17).** `/v1/session` gains `mcpAuth: true|false`. The
  Playground adds a token field held in a JS variable only; requests add
  `Authorization: Bearer`. Messages for `401`, `403`, `503` in `state.js`.
- **Flags and chart (R01, R02, R20).** `--auth`, `--auth-resource-url`,
  `--auth-issuers` (JSON array, from a ConfigMap-free value rendered into
  the args), `--auth-required-scopes`. Chart values under `gateway.auth`
  with schema: `resourceURL` `format: uri` pattern `^https://`, issuers
  array of objects with `additionalProperties: false`; template `fail` when
  enabled without `resourceURL` or issuers, or with an `http` issuer lacking
  `allowHTTP`.
- **End to end.** `test/e2e/` gains a tiny issuer: the e2e MCP server image
  serves a JWKS and mints tokens with a test key on a debug path enabled by
  env only in e2e. `e2e-kind.sh` enables auth, checks `401` + metadata,
  valid token success, wrong-audience `401`, missing scope `403`.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/clientauth/` | New authenticator, key sets, metadata, tests |
| `internal/gateway/api/api.go`, `traffic.go` | Wiring, refusals, outcomes, `/v1/routes` sanitising |
| `internal/gateway/auth/auth.go` | `/v1/session` `mcpAuth` |
| `internal/gateway/console/static/` | Playground token, notices |
| `cmd/gateway/main.go` | Flags |
| `go.mod`, `go.sum` | `go-jose/v4` |
| Gateway chart values, schema, Deployment, tests | `gateway.auth` |
| `test/e2e/mcp-server/main.go`, `scripts/e2e-kind.sh` | Test issuer, auth section |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0006-R01, R02 | `api_test.go` auth off; `cmd/gateway` config test; `gateway_test.yaml` failures |
| SPEC-0006-R03, R04, R05, R06 | `clientauth` and `api_test.go` refusal cases |
| SPEC-0006-R07 | `api_test.go` metadata document and `404` |
| SPEC-0006-R08, R09 | `clientauth` token matrix (alg, iss, kid, exp, nbf, aud) |
| SPEC-0006-R10, R11, R12 | `clientauth` key set tests with fake transport and clock |
| SPEC-0006-R13 | Existing strip tests extended with auth on |
| SPEC-0006-R14 | Log capture and traffic assertions |
| SPEC-0006-R15, R16, R17 | `auth_test.go`; console smoke test; `console_test.go` storage scan |
| SPEC-0006-R18, R19 | `api_test.go` route listing |
| SPEC-0006-R20 | `clientauth` config tests; `gateway_test.yaml` |
| All | `e2e-kind.sh` authentication section |

## Rollout

Off by default until `1.0.0-beta.1`. Release notes: how to enable, values,
issuer requirements, Playground token, `/v1/routes` endpoint redaction.

## Risks

- Authorization servers differ in `aud` handling (some need a resource
  indicator, RFC 8707). Mitigation: the guide covers Keycloak, Entra ID,
  and Okta audience settings.
- Clock skew between clusters and IdPs. Mitigation: 60 s leeway and a log
  line (no token) when `exp`/`nbf` fail by less than 5 minutes.
