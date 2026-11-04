# SPEC-0042 plan: MCP sessions and streams bound to their caller

## Design

Two pull requests, each test-first: the tests of the first step show the behavior
described under Problem in the spec before anything changes.

**Streams (R01, R02, R09).** `clientauth.Identity` gets the token's expiry, which
`Authenticate` already checks. For a `GET` whose answer is an event stream, the proxy
runs the copy under a deadline of the expiry plus `clockSkew`, and on that deadline
returns normally, so the client sees the stream end and not a broken connection.
`token_expired` joins the outcomes in `traffic.go`, and stays out of the failure sets
in `alerts/window.go` and the regular expressions in `_alerts.tpl`.

**Sessions (R03 to R08, R10, R11).** `internal/mcpsession` gets `Bind` and `Open`,
which take the key, and hold the format: version, a truncated HMAC-SHA-256, and the
ID. The HMAC's inputs are length-prefixed so that no two different callers encode the
same way. In the proxy:

- On a request, after authorization and before token exchange, a session ID that fails
  `Open` answers `404` with outcome `session_not_found`. The opened ID goes on to
  `pinSession` (`api.go`), which is unchanged, so the pod tag is checked second (R08).
- On a response, after `mcpsession.Wrap` adds the pod tag, `Bind` wraps the result, so
  the tag covers the pod tag.

`Authenticate` refuses a token with neither subject nor client ID (R11). The Gateway
reads the key once at start from a kubelet-mounted file (`--session-key-file`), refuses
to start with client authentication on when it is missing or under 32 bytes, and
`cmd/gateway/main.go` passes it to the handler. Changing the key takes a restart, which
keeps every replica on one key.

The chart follows `console-auth-secret.yaml`: `lookup`, `randAlphaNum`, and
`helm.sh/resource-policy: keep`, with `gateway.auth.sessionKeySecret` for an existing
Secret. It gets a Secret of its own, because the console's does not exist when the
console login is off.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/clientauth/clientauth.go` | `Identity` expiry; refuse a token with neither subject nor client ID |
| `internal/gateway/api/api.go` | Stream deadline; check and add the session tag |
| `internal/gateway/api/traffic.go` | Outcome `token_expired` |
| `internal/gateway/alerts/window.go`, `charts/kavrynt/templates/_alerts.tpl` | Confirm the outcome is not a failure |
| `internal/mcpsession/mcpsession.go` | `Bind`, `Open` |
| `cmd/gateway/main.go` | `--session-key-file`; refuse to start without the key |
| `charts/kavrynt/charts/gateway/templates/` (`session-key-secret.yaml`, `deployment.yaml`), `values.yaml`, `values.schema.json`, umbrella values and schema | Secret, mount, flag, value |
| `charts/kavrynt/tests/` | Secret kept across upgrades, existing Secret, mount |
| `scripts/e2e-kind.sh`, `docs/E2E.md`, `CLAUDE.md` | Contract: another caller's session ID gets `404`, and a stream ends at expiry |
| `docs/components/gateway/README.md`, `charts/kavrynt/README.md`, `SECURITY.md` | Docs, and remove the two known limitations |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0042-R01 | `internal/gateway/api/session_test.go` `TestStreamEndsWhenItsTokenExpires`, `scripts/e2e-kind.sh` |
| SPEC-0042-R02 | `internal/gateway/api/session_test.go` `TestExpiredStreamOutcome` |
| SPEC-0042-R03 | `internal/mcpsession/mcpsession_test.go` `TestBindCoversCallerRouteAndSession` |
| SPEC-0042-R04 | `internal/gateway/api/session_test.go` `TestAnotherCallersSessionIsNotFound`, `scripts/e2e-kind.sh` |
| SPEC-0042-R05 | `internal/gateway/api/affinity_test.go` `TestBoundSessionKeepsItsPod` |
| SPEC-0042-R06 | `charts/kavrynt/tests/`, `cmd/gateway` start-up test |
| SPEC-0042-R07 | `internal/gateway/api/session_test.go` `TestSessionKeyInputsAreNotExposed` |
| SPEC-0042-R08 | `internal/gateway/api/affinity_test.go` `TestChangedPodTagIsNotFound` |
| SPEC-0042-R09 | `internal/gateway/alerts/window_test.go`, `charts/kavrynt/tests/alerts_test.yaml` |
| SPEC-0042-R10 | `internal/mcpsession/mcpsession_test.go` `TestUnknownVersionDoesNotOpen` |
| SPEC-0042-R11 | `internal/gateway/clientauth/clientauth_test.go` `TestTokenWithoutCallerIsRefused` |

## Rollout

No switch: both behaviors follow client authentication, and routes with it off are
unchanged. Release notes state the compatibility section of the spec, and that GitOps
installs must set `gateway.auth.sessionKeySecret` before upgrading.

## Risks

- Short-lived tokens make streams end often, and messages can be lost on servers that
  do not support resuming. `token_expired` in the metrics shows how often.
- The rolling restart check of the end-to-end contract must keep passing with sessions
  in use, since it is the case that proves replicas share the key.
- A client that changes its client ID, or an issuer that omits `sub`, is affected in
  a way the spec describes but no test against a real issuer covers. The first shows as
  `session_not_found`, the second as `401`.
