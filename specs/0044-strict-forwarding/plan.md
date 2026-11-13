# SPEC-0044 plan: Only MCP requests and answers pass the Gateway

## Design

Everything lives in the Gateway's proxy (`internal/gateway/api`); the
Operator and `kavryctl` do not change.

- **Request shape (R01 to R03).** `checkRequestShape` looks only at the
  method, `Content-Length`, and `Content-Type`, so it runs right after the
  route lookup and before authentication. Registered and unregistered
  routes get the same answer; only a registered route is counted. The
  answer carries `Connection: close`, so the server does not drain the body
  either. Since a form never gets past this check, `tokenOutsideHeader` no
  longer reads a body: it only looks for `access_token` in the query.
- **Overrides (R04).** The three override headers are deleted after the
  request headers are copied, apart from the configurable strip list, so
  they do not count as stripped credentials in the metrics.
- **Names (R05, R06).** `checkNames` uses `mcpmeta.ValidName`, the rule the
  Operator applies to recorded tool names, on the method and on a
  `tools/call` name. It runs after the protocol checks and before the
  policy, when a policy is on or `strictProtocol` is. The error never
  quotes the name.
- **Redirects (R07).** The proxy client already does not follow redirects;
  a `3xx` now becomes `502` before any header or body is copied.
- **Response headers (R08, R09).** `copyResponseHeaders` copies only the
  fixed list plus `--allow-response-headers`, still dropping headers named
  in `Connection`. `AllowResponseHeaders` refuses names in the fixed refusal
  list and any `Access-Control-*`, case-insensitively; the chart refuses
  the same names at render time.
- **Records (R10).** Refusals use the existing `protocol_error` and
  `upstream_error` outcomes and the `invalid` audit decision.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/api/forwarding.go` | New: shape and name checks, response header lists, `AllowResponseHeaders` |
| `internal/gateway/api/api.go` | Calls the checks; override headers; redirects; response header copy; `tokenOutsideHeader` reads no body |
| `cmd/gateway/main.go` | `--allow-response-headers` (`KAVRYNT_ALLOW_RESPONSE_HEADERS`) |
| `charts/kavrynt/charts/gateway/` | `config.allowResponseHeaders` value, schema, render-time check, Deployment argument |
| `specs/0006-mcp-client-authentication/` | R04 withdrawn, R21 added |
| `internal/gateway/api/*_test.go` | `newRequest` sends POSTs as `application/json`, like MCP clients; tests of replaced behavior updated |
| `scripts/e2e-kind.sh` | Form, non-JSON, `GET` body, and name refusals in the cluster |
| Docs | Gateway component docs, chart README, `ROADMAP.md`, `CLAUDE.md` |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0044-R01 | `forwarding_test.go` `TestBodiesOnGetAndDeleteAreRefused`; `size_test.go` keeps `413` for an oversized `DELETE`; `scripts/e2e-kind.sh` |
| SPEC-0044-R02 | `forwarding_test.go` `TestPostsMustBeJSON`; `clientauth_test.go` `TestTokensOutsideTheHeaderAreRejected`; `scripts/e2e-kind.sh` |
| SPEC-0044-R03 | `forwarding_test.go` `TestShapeIsCheckedBeforeAuthenticationWithoutReading`; `body_deadline_test.go`; `scripts/e2e-kind.sh` |
| SPEC-0044-R04 | `forwarding_test.go` `TestMethodOverrideHeadersAreNotForwarded` |
| SPEC-0044-R05, R06 | `forwarding_test.go` `TestNamesARuleCannotMatchAreRefused`; `scripts/e2e-kind.sh` |
| SPEC-0044-R07 | `forwarding_test.go` `TestRedirectsAreNotPassedOn`; `security_test.go` `TestProxyNeitherFollowsNorPassesOnRedirects` |
| SPEC-0044-R08 | `forwarding_test.go` `TestOnlyMCPResponseHeadersReachClients` |
| SPEC-0044-R09 | `forwarding_test.go` `TestAllowedResponseHeaders`; `charts/kavrynt/tests/gateway_response_headers_test.yaml` |
| SPEC-0044-R10 | `forwarding_test.go` `TestRefusalsAreRecordedAsProtocolErrors`, `TestRedirectsAreNotPassedOn` |
| SPEC-0006-R21 | `clientauth_test.go` `TestTokensOutsideTheHeaderAreRejected`; `scripts/e2e-kind.sh` |

## Rollout

On by default, with no switch for R01 to R04 and R07: each closes a way
around the policy or the client's sign-in. R05 can be turned off only
without a policy (`gateway.config.strictProtocol: false`); R08 is widened
with `gateway.config.allowResponseHeaders`. Release notes: the entries in
the spec's Compatibility section.

## Risks

- A client that `POST`s without `Content-Type: application/json` breaks.
  The MCP SDKs, the console, and the e2e clients send it; the refusal names
  the header, and it is counted as `protocol_error`.
- A server that relies on a redirect or on a custom response header breaks
  with `502` or a missing header. Both show in the Gateway's metrics and
  the release notes name the fix.
