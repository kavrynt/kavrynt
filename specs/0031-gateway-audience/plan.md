# SPEC-0031 plan: Gateway audience

## Decisions

- The list takes route names and `<namespace>.*` patterns, not label
  selectors (open question 1, as recommended).
- The Gateway audience is `resourceURL` exactly, as the chart passes it
  (trailing slash removed); `resourceURL/` and `resourceURL/mcp` are not it
  (open question 2, as recommended).
- `audience` in audit events is omitted (`omitempty`) unless the Gateway
  audience is enabled, so installations that do not enable it write the
  same events as before and older `kavryctl audit verify` keeps verifying
  them (Compatibility). The policy input always has it.
- With the Gateway audience on, the console notice is shown from loopback
  too, since it names what one token reaches.

## Design

- **Authentication (R02, R03).** `clientauth.Config.GatewayAudienceRoutes`
  holds the list; `Validate` refuses entries other than
  `<namespace>.<name>` and `<namespace>.*` (so `*` too). `Authenticate`
  validates issuer and time with go-jose, then decides the audience itself:
  the route's own URI gives `route`; otherwise `resourceURL` for a route the
  list covers gives `gateway`; anything else is `invalid_token`. Scopes are
  checked after, as before. A route's namespace ends at its first dot
  (namespaces are DNS labels).
- **Identity (R04, R05).** `clientauth.Identity.Audience` carries the
  result. `policy.Input.Audience` copies it; `auditv1.Event.Audience`
  (`audience,omitempty`) copies it only when the Authenticator has a
  Gateway audience.
- **Visibility (R06).** `--auth-gateway-audience-routes` (comma-separated)
  configures it; the Gateway refuses it without `--auth`. At start the
  Gateway logs a warning with the audience and routes. `GET /v1/session`
  reports `gatewayAudience` (a list, `[]` when off) to signed-in sessions
  and when console login is off, never in its `401` to callers without a
  session; `state.js` reads it, `notice` names the routes, and
  `noticeShown` keeps the notice visible on loopback while it is on.
- **Chart (R01, R02).** `gateway.auth.gatewayAudience.enabled` and
  `.routes` in the gateway subchart values and schema (entries are route
  names, namespace patterns, or `*`, so that `*` reaches the template's own
  message). `gateway.clientAuthEnabled` fails rendering when it is on
  without `gateway.auth.enabled`, with no routes, or with `*`.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/clientauth/clientauth.go` | Route list, audience decision, `Identity.Audience` |
| `internal/gateway/policy/policy.go` | `Input.Audience` |
| `api/audit/v1/event.go` | `Event.Audience` (optional) |
| `internal/gateway/api/api.go`, `audit.go` | Policy input, audit field, `/v1/session` without login |
| `internal/gateway/auth/auth.go` | `/v1/session` with login |
| `internal/gateway/console/static/state.js`, `app.js` | Notice |
| `cmd/gateway/main.go` | Flag, start-up warning |
| `charts/kavrynt/charts/gateway/` | Values, schema, checks, argument |
| Docs | Gateway component docs, `docs/AUTH-SETUP.md`, chart README, `SECURITY.md` |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `clientauth.TestGatewayAudienceIsOffByDefault`, `main.TestGatewayAudienceConfiguration`, `clientauth_test.yaml` |
| R02 | `clientauth.TestGatewayAudienceReachesListedRoutes`, `clientauth.TestGatewayAudienceKeepsOtherChecks`, `clientauth.TestGatewayAudienceRouteList`, `api.TestPolicySeesTheTokenAudience`, `clientauth_test.yaml` |
| R03 | `clientauth.TestGatewayAudienceReachesListedRoutes` |
| R04 | `clientauth.TestGatewayAudienceReachesListedRoutes`, `api.TestPolicySeesTheTokenAudience` |
| R05 | `api.TestAuditRecordsTheTokenAudience` |
| R06 | `main.TestGatewayAudienceConfiguration`, `auth.TestSessionReportsGatewayAudience`, `api.TestSessionReportsGatewayAudience`, `test/console/state.test.mjs` |

The Kind e2e does not cover it yet; the unit tests run the whole request
path with signed tokens.

## Rollout

Off by default (`gateway.auth.gatewayAudience.enabled: false`). Turn it off
by setting it back to `false`: tokens for the Gateway audience are refused
again at once. Release notes: "Opt-in Gateway audience: one token can reach
a listed group of MCP routes (SPEC-0031)."

## Risks

- A list broader than intended lets a leaked token reach more servers.
  Detected by the start-up warning, the console notice, and
  `audience: gateway` in audit events; contained by policies that deny
  `gateway` for sensitive routes.
- Events with `audience` fail verification by a `kavryctl` older than this
  release, like `filtered_tools`; the component docs say to verify with the
  Gateway's release.
