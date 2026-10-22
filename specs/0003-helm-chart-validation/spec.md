# SPEC-0003: Helm chart values validation and unit tests

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-22 |
| Issue | none |
| ADRs | [ADR-0001](../../docs/ADR-0001-Runtime-Monorepo.md) |
| Supersedes | none |

## Summary

The `kavrynt` chart validates user values with JSON schemas and has
`helm-unittest` tests for the rendered resources that the security model
depends on. A mistyped or malformed value fails at `helm install`,
`helm upgrade`, or `helm template` instead of being ignored or crashing the
Gateway or Operator at start.

## Problem

- Helm silently ignores unknown keys, so `gateway.consle.enabled=false`
  leaves the console on without any warning.
- Values of the wrong shape (an origin with a path, a duration without a
  unit) render fine and only fail when the container starts, or never match
  at all.
- Chart guarantees such as read-only Gateway RBAC and non-root containers are
  checked only by the Kind end-to-end test, which is slow and runs on pull
  requests only.

## Goals

- Reject unknown keys and malformed values before anything is applied.
- Test chart rendering in seconds, locally and in every CI run.

## Non-goals

- Validating Kubernetes-native objects passed through as-is (`resources`,
  `affinity`, `tolerations`, security contexts). The API server validates
  them.
- Generating the schema from annotations. The schemas are hand-written, like
  the CRD, and strict enough that a new value without a schema entry fails
  `make helm-lint`.

## Requirements

- **SPEC-0003-R01** The chart MUST reject unknown keys at the top level and
  in every `gateway` and `operator` values object it defines, so typos fail.
  `global` MUST stay open, because platforms inject their own globals.
- **SPEC-0003-R02** The chart MUST reject values of the wrong type, such as a
  string `gateway.replicaCount` or a non-boolean `gateway.console.enabled`,
  and an `image.pullPolicy` other than `Always`, `IfNotPresent`, or `Never`.
- **SPEC-0003-R03** The chart MUST reject `gateway.config.allowedOrigins`
  entries that are not `http(s)://host[:port]` with an optional trailing
  slash, including entries with a path or a comma.
- **SPEC-0003-R04** The chart MUST reject durations that Go cannot parse in
  `gateway.config.requestTimeout`, `gateway.metrics.serviceMonitor.interval`,
  `operator.config.toolDiscovery.refreshInterval`, and
  `operator.cloud.syncInterval`.
- **SPEC-0003-R05** The chart MUST reject `global.watchNamespaces` entries
  that are not valid namespace names, and duplicate entries.
- **SPEC-0003-R06** By default the Gateway MUST get one ClusterRole that
  grants only `get`, `list`, and `watch` on `mcpservers.kavrynt.io`. With
  `global.watchNamespaces` set, it MUST get one Role and RoleBinding per
  listed namespace with the same rules and no ClusterRole.
- **SPEC-0003-R07** With default values, the Gateway and Operator containers
  MUST run as non-root with a read-only root filesystem, no privilege
  escalation, and all capabilities dropped, and the Gateway service account
  MUST disable token automounting.
- **SPEC-0003-R08** The Gateway MUST be started with `--console=true` by
  default and `--console=false` when `gateway.console.enabled` is `false`.
- **SPEC-0003-R09** `gateway.config.allowedOrigins` and
  `gateway.config.stripRequestHeaders` MUST reach the Gateway as
  comma-separated `--allowed-origins` and `--strip-request-headers` flags.
- **SPEC-0003-R10** Enabling `operator.cloud` without an `https` URL or a
  token Secret name MUST fail rendering. When valid, the token MUST be
  mounted read-only from the named Secret and key.
- **SPEC-0003-R11** Setting the removed `gateway.config.watchNamespaces` MUST
  fail rendering with a message that names `global.watchNamespaces`.

## Acceptance criteria

- Given `--set gateway.consle.enabled=false`, when installing, then Helm
  fails before applying anything (R01).
- Given `--set gateway.config.allowedOrigins[0]=https://kavrynt.example.com/ui`,
  when installing, then Helm fails (R03).
- Given default values, when running `make helm-unittest`, then the RBAC,
  security context, and flag tests pass (R06–R09).

## Security and privacy

Strict validation fails closed: a mistyped security setting is an error, not
a silently ignored key. The tests pin the Gateway's read-only RBAC and the
container hardening, so a template change that weakens them fails in CI.

## Compatibility

Installs that set unknown or malformed values, which had no effect or broke
at start before, now fail at install. Release notes must say so. No CRD
change.

## Documentation

`CONTRIBUTING.md` (testing and where changes go), the chart README values
tables, and `docs/RELEASE.md`.

## Open questions

None.
