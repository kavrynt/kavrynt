# SPEC-0005 plan: Console admin login

## Design

- **Package `internal/gateway/auth`.** Owns everything about the login:
  reading the password and signing key from files, signing and verifying
  sessions, the per-address failure limiter, and the `/v1/session` handler.
  `api.Handler` gets `EnableConsoleAuth(*auth.Authenticator)` and asks it
  `Authorized(r)` before serving `/v1/servers` and `/v1/traffic`. Without
  that call the handler behaves as before (R03), which also keeps the
  SPEC-0001 tests unchanged.
- **Credentials.** `--console-auth-dir` holds files `password` and
  `sessionKey` (Secret keys). They are read on each use, so a rotated Secret
  takes effect once the kubelet updates the mount, with no restart (R06).
  Missing or empty files fail closed: logins get `503`, sessions are
  invalid (R11).
- **Session token.** `base64url(payload) "." base64url(mac)`, payload
  `{"sub":"admin","exp":<unix>}`, `mac = HMAC-SHA256(sessionKey, payload ||
  SHA-256(password))`. The random key prevents offline guessing of the
  password from a cookie; the password digest ties the session to the
  current password (R06). Verification uses `hmac.Equal`.
- **Password check.** `subtle.ConstantTimeCompare` of the SHA-256 digests of
  the submitted and stored passwords, so length does not leak; the
  username is compared the same way (R04).
- **Limiter.** Failures per client host (`RemoteAddr`) in a one-minute
  window; the sixth attempt in the window gets `429` with `Retry-After`. The
  map is bounded (10,000 addresses; cleared when full) so it cannot grow
  without limit (R09).
- **Cookie.** `kavrynt_session`, `Path=/v1/`, `HttpOnly`,
  `SameSite=Strict`, `Max-Age` = lifetime, `Secure` on TLS or
  `X-Forwarded-Proto: https` (R05). Logout sets `Max-Age=0` (R07).
- **Chart.** `templates/console-auth-secret.yaml` creates
  `<gateway fullname>-console-admin` with `randAlphaNum 32` and
  `randAlphaNum 64`, reusing existing values through `lookup` and annotated
  `helm.sh/resource-policy: keep` (R12). The Deployment mounts the Secret
  (or `existingSecret`) at `/etc/kavrynt/console-auth`, read-only, mode
  `0444` (the files are root-owned and the Gateway runs as a non-root user
  without an fsGroup), and passes the flags (R13). RBAC is unchanged.
- **Console.** `app.js` checks `GET /v1/session` before drawing. A `401`
  from any data poll switches to the login view; a successful login reloads
  the data. The sidebar shows `admin` and Log out. The no-login notice
  changes wording when login is on (R14–R16). Login logic that can be pure
  (`loginError` messages) lives in `state.js`.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/auth/` | New: credentials, sessions, limiter, handler, tests |
| `internal/gateway/api/api.go`, `api_test.go` | Route `/v1/session`; guard `/v1/servers` and `/v1/traffic` |
| `cmd/gateway/main.go` | `--console-auth`, `--console-auth-dir`, `--console-session-ttl` |
| `internal/gateway/console/static/*` | Login view, user menu, 401 handling, notice wording |
| `charts/kavrynt/charts/gateway/` | Values, schema, Secret template, Deployment mount and flags |
| `charts/kavrynt/tests/gateway_test.yaml` | Secret, mount, flags, existing Secret, disabled login |
| `scripts/e2e-kind.sh`, `scripts/demo-kind.sh` | Sign in with the generated password; check 401 and `/mcp/` unaffected |
| `test/console-smoke/` | Playwright smoke test of the console, including login (also SPEC-0002) |
| `docs/components/gateway/README.md`, chart README, `README.md`, `docs/DEMO.md`, `docs/E2E.md` | Login, what it does not protect |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0005-R01, R02, R03 | `api_test.go` guarded and unguarded endpoints with and without auth; `e2e-kind.sh` 401 and `/mcp/` without cookie |
| SPEC-0005-R04, R05, R07, R08 | `auth_test.go` handler cases |
| SPEC-0005-R06 | `auth_test.go` password rotation on disk |
| SPEC-0005-R09 | `auth_test.go` limiter with a fake clock |
| SPEC-0005-R10 | `auth_test.go` captured log has no password or token |
| SPEC-0005-R11 | `auth_test.go` missing files; `cmd/gateway` start-up check |
| SPEC-0005-R12, R13 | `gateway_test.yaml` Secret, keep policy, mount, flags, existing Secret; `e2e-kind.sh` RBAC check and login with the Secret |
| SPEC-0005-R14, R15, R16 | `test/console-smoke/smoke.mjs`; `console_test.go` storage scan |

## Rollout

On by default (`gateway.console.auth.enabled: true`). Turn off with
`gateway.console.auth.enabled=false` or `--console-auth=false`. Release
notes: the console now requires the admin password; how to read it; scripts
using `/v1/servers` or `/v1/traffic` must sign in; MCP routes are unchanged
and still unauthenticated.

## Risks

- GitOps renders (`helm template`) cannot `lookup` the existing Secret and
  would generate a new password on every render. Mitigation: the keep
  policy stops deletion, the chart README tells GitOps users to set
  `existingSecret`, and a render without cluster access is detectable only
  by the operator, so the docs call it out.
- Operators may read "login" as "the Gateway is protected". Mitigation: R16
  on screen and explicit wording in every doc that mentions the login.
