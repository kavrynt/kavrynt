# SPEC-0019: kavryctl upgrade and rollback

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0001](../../docs/ADR-0001-Runtime-Monorepo.md) |
| Supersedes | none |

## Summary

`kavryctl upgrade --version <v>` and `kavryctl rollback` run the tested Helm
upgrade and rollback path (SPEC-0009) with checks before and after: they
find the release, adopt a CRD installed by `0.0.5-beta.1` or earlier when
asked, upgrade with the release's existing values, wait for the Gateway and
the Operator, and confirm every routed server is still routed.

## Problem

Upgrades are a sequence of Helm and `kubectl` steps with a one-time CRD
adoption; mistakes leave a half-upgraded release. Platform teams want one
command that checks the outcome.

## Goals

- One command to upgrade or roll back, with a clear report.
- No change to the cluster when a preflight check fails.

## Non-goals

- Replacing Helm or managing values (the release's values are reused).
- GitOps-managed installs (they upgrade through their own pipeline).

## Requirements

- **SPEC-0019-R01** `kavryctl upgrade --version <v> [--release kavrynt]
  [--namespace kavrynt-system]` MUST require the `helm` binary and fail with
  a clear message without it.
- **SPEC-0019-R02** Before changing anything it MUST check that the release
  exists, the target version exists in the chart repository, and the CRD is
  owned by the release; if not owned, it MUST stop and explain unless
  `--adopt-crd` is given.
- **SPEC-0019-R03** It MUST record the routed servers (from `MCPServer`
  status), run `helm upgrade --reset-then-reuse-values --version <v> --wait`
  (amended: `--reuse-values` keeps the previous chart's defaults, including
  its image tags, so the upgrade would not take effect), and after
  the rollout MUST report any server that was routed before and is not now;
  it MUST exit non-zero in that case.
- **SPEC-0019-R04** `kavryctl rollback [--revision N]` MUST run `helm
  rollback` with the same before and after checks.
- **SPEC-0019-R05** `--dry-run` MUST print the checks and the Helm command
  without running it.
- **SPEC-0019-R06** kavryctl MUST NOT import Gateway or Operator internals
  (CLAUDE.md component boundaries).

## Acceptance criteria

- Given a `0.0.5-beta.1` install, when `kavryctl upgrade --version <next>`
  runs without `--adopt-crd`, then it stops before changing the cluster and
  prints the adoption step (R02).
- Given `--adopt-crd`, then it adopts, upgrades, and reports every server
  still routed (R03).

## Security and privacy

Runs with the user's kubeconfig and Helm permissions; adds none. Values are
reused, never printed (they may hold secrets).

## Compatibility

New commands; no change to existing ones.

## Documentation

kavryctl component doc, chart README (upgrading), README.

## Decisions

- `kavryctl` runs the `helm` binary instead of embedding the Helm SDK, which
  would add a large dependency tree to every `kavryctl` download.
- `--chart` names the chart (default `oci://ghcr.io/kavrynt/charts/kavrynt`;
  a local directory is accepted for tests and air-gapped installs), and
  `--values FILE` passes extra values on top of the reused ones.
- "Routed" means `Ready=True`, the same rule `kavryctl list` uses for the
  ROUTE column.
