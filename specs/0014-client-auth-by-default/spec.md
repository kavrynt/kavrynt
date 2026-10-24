# SPEC-0014: MCP client authentication on by default

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("Version `1.0.0-beta.1` is reserved for when authentication, policy, and audit are implemented") |
| Supersedes | none |

## Summary

From `1.0.0-beta.1`, a chart install requires MCP client authentication
(SPEC-0006) unless the installer turns it off explicitly. An install that
names neither trusted issuers nor `gateway.auth.enabled=false` fails with a
message that offers both, so no Gateway starts with open MCP routes by
accident.

## Problem

Authentication, policy, and audit exist but are off by default. A default
install still lets anyone who reaches the Gateway call every tool. ADR-0003
reserves `1.0.0-beta.1` for the release where that is no longer the default.

## Goals

- A default install never exposes unauthenticated MCP routes without an
  explicit choice.
- Trials stay one command: the docs show the explicit opt-out.
- Upgrades from `0.0.x` cannot silently open or close routes.

## Non-goals

- Turning tool authorization policy or audit on by default (a policy must
  be written first; audit needs a log pipeline).
- Signing the console in through the authorization server.

## Requirements

- **SPEC-0014-R01** The chart MUST default `gateway.auth.enabled` to `true`.
- **SPEC-0014-R02** With `gateway.auth.enabled=true` and no
  `gateway.auth.resourceURL` or no issuer, rendering MUST fail with one
  message that names both ways forward: configure `resourceURL` and
  `issuers`, or set `gateway.auth.enabled=false` to run with open MCP routes.
- **SPEC-0014-R03** With `gateway.auth.enabled=false`, the console notice and
  the Gateway's start-up log MUST say that MCP routes are unauthenticated.
- **SPEC-0014-R04** `helm upgrade` from a `0.0.x` release that never set
  `gateway.auth` MUST fail with the R02 message (no release changes) until
  one of the two is chosen.
- **SPEC-0014-R05** The README quick start, the chart README, and
  `docs/DEMO.md` MUST show both an authenticated install and the explicit
  opt-out for trials.
- **SPEC-0014-R06** `make e2e-kind` MUST test both paths: an install with the
  explicit opt-out, and authentication on with the test authorization
  server. It MUST also show that an install with default values and an
  upgrade from the previous release without `gateway.auth` values both fail
  with the R02 message and change nothing.

## Acceptance criteria

- Given `helm install kavrynt ...` with no values, then rendering fails
  naming `gateway.auth.issuers` and `gateway.auth.enabled=false` (R01, R02).
- Given `--set gateway.auth.enabled=false`, then the install succeeds and the
  Gateway logs a warning that MCP routes are unauthenticated (R03).
- Given a `0.0.6-beta.1` release with default values, when `helm upgrade` to
  `1.0.0-beta.1` runs without new values, then it fails and the running
  release is unchanged (R04).

## Security and privacy

Fails closed: the unsafe configuration needs a deliberate value. No new
access or data.

## Compatibility

Breaking for installs that rely on the default: they must add issuers or
`gateway.auth.enabled=false`. The release notes lead with it. The Gateway
binary's `--auth` default stays `false`; only the chart changes.

## Documentation

README quick start, chart README (upgrading to `1.0.0-beta.1`), DEMO,
release notes, CLAUDE.md "What works today".

## Decisions

- The opt-out is `gateway.auth.enabled=false`, not a separate value.
  `helm upgrade --reuse-values` keeps only values the user set, so an
  upgrade cannot inherit the old default; a values file that sets
  `enabled: false` records a choice someone made.
- The chart does not refuse `LoadBalancer` or `NodePort` Services while
  authentication is off; the start-up warning and the console notice say
  the routes are open, and NetworkPolicies (SPEC-0007) limit who reaches
  the Gateway.
- The e2e keeps its first install unauthenticated (with the explicit
  opt-out) and turns authentication on by upgrade, because every earlier
  check (load clients, NetworkPolicy probes, the browser smoke test) calls
  routes without tokens.
