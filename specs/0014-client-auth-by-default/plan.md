# SPEC-0014 plan: MCP client authentication on by default

## Design

- **Default (R01).** `charts/kavrynt/charts/gateway/values.yaml`:
  `auth.enabled: true`. The Gateway binary keeps `--auth=false` as its
  default; only the chart changes.
- **One refusal message (R02, R04).** `gateway.clientAuthEnabled` in the
  Gateway chart's `_helpers.tpl` fails with a single message when `enabled`
  is true and `resourceURL` or `issuers` is missing:
  `gateway.auth: MCP client authentication is on by default. Set
  gateway.auth.resourceURL and gateway.auth.issuers to trust your
  authorization server, or set gateway.auth.enabled=false to run with
  unauthenticated MCP routes.` The SPEC-0006-R02 chart tests move to this
  message. An upgrade renders before it changes anything, so a `0.0.x`
  release that never set `gateway.auth` stays as it was (R04).
- **Warnings (R03).** `cmd/gateway/main.go` logs
  `slog.Warn("MCP client authentication is off: every /mcp/ route accepts
  unauthenticated calls")` at start when `--auth` is off, from a small
  `warnUnauthenticated(logger, enabled)` helper that the tests call. The
  console notice ("MCP routes are not authenticated.") exists since
  SPEC-0006-R15; its console test also cites R03. The chart's `NOTES.txt`
  repeats the warning when `auth.enabled` is false.
- **Docs (R05).** README quick start, chart README (install, a section on
  upgrading to `1.0.0-beta.1`), `docs/DEMO.md`, and
  `examples/helm/values-production.yaml` show both the authenticated
  install and `--set gateway.auth.enabled=false`. `scripts/demo-kind.sh` and
  `scripts/e2e-release.sh` install with the explicit opt-out.
- **E2E (R06).** `scripts/e2e-kind.sh`:
  - a server-side dry-run install with default values fails with the R02
    message;
  - the first install passes `--set gateway.auth.enabled=false` (and the
    Gateway log has the R03 warning); authentication is turned on later by
    upgrade, as today;
  - the turn-off upgrade becomes `gateway.auth.enabled=false`;
  - upgrade from the previous release: first without `gateway.auth`
    values, which fails with the R02 message and leaves the Deployment
    generations unchanged; then with the opt-out in `LOCAL_RELEASE_ARGS`.
    The SPEC-0009 adoption check also carries the opt-out so it still
    fails on the CRD, not on authentication.

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/charts/gateway/values.yaml`, `templates/_helpers.tpl`, `templates/NOTES.txt` | Default, message, warning |
| `charts/kavrynt/tests/clientauth_test.yaml`, other chart tests and snapshots | Default on; tests that render without auth set the opt-out |
| `cmd/gateway/main.go`, `main_test.go` | Start-up warning |
| `internal/gateway/console/static/state.test.js` (or the existing notice test) | Cite R03 |
| `scripts/e2e-kind.sh`, `scripts/e2e-release.sh`, `scripts/demo-kind.sh` | Opt-out and refusal checks |
| `README.md`, `charts/kavrynt/README.md`, `docs/DEMO.md`, `examples/helm/values-production.yaml`, `docs/E2E.md`, `CLAUDE.md` | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0014-R01, R02 | `charts/kavrynt/tests/clientauth_test.yaml` (default on, one message for each missing setting) |
| SPEC-0014-R03 | `cmd/gateway/main_test.go` (warning logged when off, not when on); console notice test |
| SPEC-0014-R04 | `scripts/e2e-kind.sh` upgrade from the previous release without auth values |
| SPEC-0014-R05 | `docs/docs_test.go` (a test-only package): the README, the chart README, and `docs/DEMO.md` each show `gateway.auth.issuers` and `gateway.auth.enabled=false` |
| SPEC-0014-R06 | `scripts/e2e-kind.sh` |

## Rollout

Breaking chart default, released as `1.0.0-beta.1` (ADR-0003). Release notes
lead with the two ways forward. A `0.0.6-beta.1` cut before this change
merges ships SPEC-0006 to SPEC-0013 with authentication still opt-in.

## Risks

- Every `helm upgrade --install` without values now fails. Mitigation: the
  message names both fixes; all docs and scripts show the opt-out.
- The upgrade check depends on the previous release being reachable in the
  registry, as SPEC-0009's does.
