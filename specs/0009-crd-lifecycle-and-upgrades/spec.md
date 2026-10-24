# SPEC-0009: CRD lifecycle and upgrades

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0001](../../docs/ADR-0001-Runtime-Monorepo.md) (one chart) |
| Supersedes | none |

## Summary

`helm upgrade` updates the `MCPServer` CRD together with the Gateway and the
Operator, `helm rollback` and `helm uninstall` never delete the CRD or any
`MCPServer`, and every release is proven to upgrade from the previous
published release and roll back to it without losing a route.

## Problem

The CRD ships in the chart's `crds/` directory, which Helm installs once and
never upgrades. Every upgrade needs a manual `kubectl apply` of the right
CRD file before `helm upgrade`; forgetting it leaves an old schema that
silently prunes new status fields (for example `status.serverInfo` added in
`0.0.5-beta.1`). Nothing tests an upgrade or a rollback, so a release can
break existing installs without CI noticing.

## Goals

- One command upgrades everything: `helm upgrade`.
- Rollback and uninstall are safe for customer data (`MCPServer` objects).
- CI proves upgrade and rollback from the previous release.

## Non-goals

- `kavryctl upgrade` and `kavryctl rollback` commands (ROADMAP item 5, own
  spec).
- Conversion webhooks or a second API version; `v1alpha1` stays the only
  version.
- Backing up `MCPServer` objects: they are Kubernetes objects, covered by
  the cluster's backup (for example Velero). The docs describe it.

## Requirements

### CRD in the chart

- **SPEC-0009-R01** With `operator.crds.install=true` (default), the chart
  MUST render the `MCPServer` CRD as a regular template, so `helm install`
  and `helm upgrade` create and update it.
- **SPEC-0009-R02** With `operator.crds.keep=true` (default), the rendered
  CRD MUST carry `helm.sh/resource-policy: keep`.
- **SPEC-0009-R03** With `operator.crds.install=false`, the chart MUST NOT
  render the CRD, for installations that manage CRDs separately.
- **SPEC-0009-R04** The rendered CRD MUST be identical in content to
  `config/crd/bases/kavrynt.io_mcpservers.yaml`; the release contract MUST
  fail otherwise.

### Data safety

- **SPEC-0009-R05** `helm uninstall` and `helm rollback` to a release whose
  chart does not render the CRD MUST leave the CRD and every `MCPServer`
  in place.

### Upgrading from 0.0.x

- **SPEC-0009-R06** The chart README MUST give the one-time command that
  lets Helm adopt a CRD installed by `0.0.5-beta.1` or earlier, and
  `helm upgrade` MUST succeed after it.
- **SPEC-0009-R07** When Helm cannot adopt the CRD because that command was
  not run, the upgrade MUST fail before any workload changes, with Helm's
  ownership error naming the CRD (no partial upgrade).

### Proof

- **SPEC-0009-R08** The end-to-end test MUST install the previous published
  release from the OCI registry, register an `MCPServer`, upgrade to the
  build under test, and verify the server stays `Ready`, its route still
  answers `tools/list`, and the new schema is in place.
- **SPEC-0009-R09** It MUST then `helm rollback` to the previous release and
  verify the CRD, the `MCPServer`, and the route remain.

## Acceptance criteria

- Given a cluster running `0.0.5-beta.1` with an `MCPServer`, when the
  adoption command and `helm upgrade` to this release run, then the CRD
  has the new schema and the server is `Ready` with its route working
  (R01, R06, R08).
- Given that upgraded release, when `helm rollback` runs, then
  `kubectl get crd mcpservers.kavrynt.io` and the `MCPServer` still exist
  and the route answers (R05, R09).
- Given `operator.crds.install=false`, then no CRD is rendered (R03).
- Given a changed `config/crd/bases` file without the chart copy, then
  `make release-contract` fails (R04).
- Given the adoption command was skipped, then `helm upgrade` fails and the
  Gateway and Operator Deployments are unchanged (R07).

## Security and privacy

Helm now updates a cluster-scoped object on upgrade, so the installer needs
CRD update permission (it already needs CRD create for install). The keep
policy prevents data loss on uninstall and rollback. No runtime component
gains permissions.

## Compatibility

New values `operator.crds.install` and `operator.crds.keep`. The CRD moves
from `charts/k8s-operator/crds/` to `charts/k8s-operator/templates/`.
Existing installs run the one-time adoption command (label and annotate the
CRD) before upgrading; the release notes put it first. Fresh installs need
nothing. The CRD schema itself does not change in this spec.

## Documentation

Chart README (Upgrading, Uninstalling, Backup), `docs/RELEASE.md` (upgrade
test), CLAUDE.md (CRD locations), README quick start (drop the manual CRD
step), `docs/DEMO.md`.

## Decisions

- CRD as a template with a keep policy (the cert-manager pattern) rather
  than a pre-upgrade hook Job: no extra image, service account, or
  cluster-scoped RBAC for a hook.
- One-time manual adoption for `0.0.x` installs rather than automating it:
  all such installs are trials, and the command is two lines.
