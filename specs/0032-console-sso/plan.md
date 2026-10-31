# SPEC-0032 plan: Console sign-in through the authorization server

## Decisions

- Accepted on 2026-10-31 with both open questions as recommended: SSO has
  its own `issuer` (the console may use another provider or realm than
  agents); allowed groups do not gate parts of the console.
- Sessions reuse the console's signed cookie (SPEC-0005) and its session
  key, with a `method` field. A session counts only while its method is
  enabled: an admin session ends when SSO replaces the admin login, an SSO
  session when SSO is turned off.
- A failed code exchange answers `502`; a refused callback (state, provider
  error, missing code, unverifiable ID token) `400`; a user not allowed
  `403`.

## Design

- **Provider (`internal/gateway/clientauth/console.go`).**
  `ConsoleProvider` reuses the issuer metadata and key set code of client
  authentication. Metadata now records `authorization_endpoint`, which must
  be `https` even with `allowHTTP`. `ExchangeCode` posts the code, redirect
  URI, and PKCE verifier with `client_secret_basic`, reading the secret from
  the mounted file each time, and keeps only `id_token`. `VerifyIDToken`
  checks algorithms, key, issuer, times, `aud` (client ID), `azp` with
  several audiences, and `nonce`.
- **Flow (`internal/gateway/auth/oidc.go`).** `GET /v1/session/oidc/start`
  redirects with `state`, `nonce`, and an S256 challenge, kept with the
  verifier in a signed `HttpOnly`, `SameSite=Lax` cookie on the callback
  path for 10 minutes. `GET /v1/session/oidc/callback` clears that cookie,
  checks it, exchanges, verifies, applies the allow lists, signs the
  session, and redirects to `/ui/`. Pages are fixed text; logs carry
  reasons, never codes or tokens.
- **Admin login.** `POST /v1/session` answers `404` without
  `keepAdminLogin`; `GET /v1/session` reports `adminLogin`, the `oidc` offer
  (provider name and start path), and the session's `method`.
- **Gateway.** `--console-oidc` (JSON, unknown fields refused) and
  `--console-oidc-secret-file`; it refuses SSO without the console login, a
  redirect URL that is not the https callback, or no allow list, and warns
  at start with `keepAdminLogin`.
- **Chart.** `gateway.console.auth.oidc.*` in the values and schema;
  `gateway.consoleOIDCEnabled` refuses incomplete settings; the settings are
  passed as JSON without `clientSecret`, whose Secret is mounted at
  `/etc/kavrynt/console-oidc`.
- **Console.** `state.js` reads the offer, the method, and `adminLogin`;
  the SSO link is always the Gateway's start path. The login page shows
  "Sign in with ...", without the password form when the admin login is
  off; the header says how the person signed in.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/clientauth/console.go`, `keys.go` | Provider |
| `internal/gateway/auth/oidc.go`, `auth.go` | Flow, sessions, admin login |
| `internal/gateway/api/api.go` | Routing of `/v1/session/oidc/` |
| `cmd/gateway/main.go` | Flags |
| `charts/kavrynt/charts/gateway/` | Values, schema, checks, args, mount |
| `internal/gateway/console/static/state.js`, `app.js` | Login page, header |
| Docs | Gateway component docs, `docs/AUTH-SETUP.md`, chart README, `SECURITY.md`, `README.md`, `ROADMAP.md`, `CLAUDE.md`, `docs/DEMO.md` |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `console_auth_test.yaml`, `main.TestConsoleOIDCConfiguration` |
| R02 | `console_auth_test.yaml`, `main.TestConsoleOIDCConfiguration` |
| R03 | `auth.TestSignInStart`, `auth.TestSignInStartNeedsTheProvider`, `clientauth.TestConsoleAuthorizationEndpoint`, `test/console/state.test.mjs` |
| R04 | `auth.TestSignInStart`, `auth.TestSignInCallbackRefusals` |
| R05 | `auth.TestSignInCallbackRefusals`, `auth.TestSignInSucceedsForAllowedGroups`, `clientauth.TestConsoleExchangeCode` |
| R06 | `clientauth.TestConsoleVerifyIDToken`, `auth.TestSignInChecksTheAllowLists` |
| R07 | `auth.TestSignInSucceedsForAllowedGroups`, `auth.TestSignInChecksTheAllowLists` |
| R08 | `auth.TestSignInSucceedsForAllowedGroups`, `auth.TestSignInChecksTheAllowLists` |
| R09 | `auth.TestSignInLogsNoSecrets`, `auth.TestSignInSucceedsForAllowedGroups`, `clientauth.TestConsoleExchangeCode` |
| R10 | `auth.TestAdminLoginWithSSO`, `main.TestConsoleOIDCConfiguration` |
| R11 | `auth.TestSignInSucceedsForAllowedGroups`, `auth.TestAdminLoginWithSSO`, `test/console/state.test.mjs` |

The Kind e2e does not run a browser sign-in against a real provider yet;
the unit tests run the flow with signed ID tokens and a fake provider.

## Rollout

Off by default. Release notes: "Console sign-in through your OpenID Connect
provider for listed users and groups (SPEC-0032)."

## Risks

- An `http` authorization endpoint (an in-cluster provider) cannot be used
  for console sign-in; the docs say so.
- Sessions do not end at the provider's logout; their lifetime bounds them.
