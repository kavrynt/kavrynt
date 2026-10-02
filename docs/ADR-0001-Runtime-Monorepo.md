# ADR-0001: Runtime Monorepo

## Status

Accepted (2026-10-02)

## Context

The customer runtime was split across four private repositories (`kavryctl`,
`registry`, `gateway`, `operator`) plus this integration repository, which held
the umbrella chart, end-to-end tests, and frozen legacy snapshots of the same
components.

Observed costs of the split:

- The four components total about 3,700 lines of Go, yet every release needs
  coordinated tags in five repositories.
- The umbrella chart depends on sibling checkouts
  (`file://../../../gateway/charts/gateway`), so QA and release workflows
  check out four extra repositories with a cross-repository token
  (`KAVRYNT_REPO_TOKEN`).
- Removing the in-cluster registry (ADR-0002) makes the gateway depend on the
  operator's `MCPServer` API types. Across private repositories that requires
  private Go module authentication in every build.
- The legacy snapshots duplicate code that has already drifted from the
  canonical repositories.

## Decision

This repository, `kavrynt`, becomes the single source repository for the
customer runtime:

```text
kavrynt/
├── api/v1alpha1/          # MCPServer CRD Go types (shared)
├── cmd/
│   ├── kavryctl/          # client CLI entry point
│   ├── operator/          # operator entry point
│   ├── gateway/           # gateway entry point
│   └── registry/          # removed by ADR-0002 in 0.0.2-beta.1
├── internal/
│   ├── kavryctl/...
│   ├── operator/...
│   ├── gateway/...
│   └── registry/...       # removed by ADR-0002
├── charts/kavrynt/        # one chart, CRDs included, no sibling references
├── build/                 # one Dockerfile per image
├── config/                # generated CRD and RBAC manifests
├── test/e2e/              # Kind end-to-end tests
└── docs/
```

- One Go module: `github.com/kavrynt/kavrynt`. No `go.work`.
- One version for all runtime artifacts (images, chart, `kavryctl`), set by a
  `vX.Y.Z-beta.N` tag on `main`.
- Image names stay `kavrynt/gateway`, `kavrynt/operator`, `kavrynt/registry`
  (until removed) on GHCR and the Docker Hub mirror, so trial users see no
  naming change.
- Component history is imported with `git subtree` so `git log` and blame
  keep working. The source repositories are archived read-only afterwards,
  never deleted.
- The frozen legacy snapshots (`cmd/kavryctl`, `services/`, `operator/` at the
  repository root) are deleted. Their history remains in git.

## Consequences

- One CI workflow runs formatting, tests with the race detector, `go vet`,
  staticcheck, gosec, govulncheck, Helm lint and render, Trivy filesystem and
  image scans, and the Kind end-to-end test. `KAVRYNT_REPO_TOKEN` is no longer
  needed.
- Releases build all images from one commit, so the image set is consistent by
  construction rather than by a version-contract script.
- GHCR packages are linked to the old repositories. Before the first release
  from this repository, each package must grant this repository's Actions
  write access, and the `release` environment with Docker Hub secrets must
  exist here.
- `kavryctl` binaries were published as GitHub Release assets of a private
  repository, which anonymous trial users cannot download. A public,
  artifact-only distribution channel is required before the next trial
  release. This is an open decision, not solved by this ADR.
- The `LICENSE` file in this repository contains the Elastic License 2.0, a
  source-available licence. That conflicts with the rule that Kavrynt is not
  source-available. Replacing it is an owner and legal decision.
