# AGENTS.md

This repository contains Kavrynt Registry, the control-plane source of truth
for MCP server metadata, versions, lifecycle state, and future policy
references.

Kavrynt is an open-source AI infrastructure platform for running and operating
AI agents and MCP servers in production. Registry is one of the four MVP
components:

- kavryctl
- Registry
- Gateway
- Kubernetes Operator

## Branching Strategy

Use GitFlow.

- `main`: production-ready code only.
- `develop`: latest integrated development code.
- `feature/<branch-name>`: one focused feature, fix, or documentation change.
- `release/<version>`: release stabilization when needed.
- `hotfix/<branch-name>`: urgent production fixes from `main`.

Rules:

1. Keep `main` and `develop` present in every Kavrynt repository.
2. `main` is the production-ready baseline.
3. `develop` is the default branch for ongoing MVP integration.
4. Use `feature/<short-kebab-case-name>` only when the user asks for feature branch isolation or a risky change needs review separation.
5. Keep each change focused and easy to review.
6. Run local validation before asking the user to review or merge.
7. Merge tested feature branches back into `develop`.
8. Merge `develop` into `main` only for production-ready releases.
9. Do not force-push shared branches.
10. Do not commit secrets, local state, build output, or machine-specific files.

Current repository bootstrap note:

- If the remote only has `feature/registry-mvp-scaffold`, create `develop` from the current reviewed commit first.
- Create `main` from the same reviewed commit until a separate production release baseline exists.
- After the user confirms the local changes, the user will push branches and workflow fixes to GitHub.

Suggested first-time branch bootstrap:

```bash
git switch feature/registry-mvp-scaffold
git switch -c develop
git push -u origin develop
git switch -c main
git push -u origin main
git switch develop
```

## Codex Operating Rules

- Create and update files inside the repository only unless the user explicitly asks for commits or pushes.
- Do not push to remote for this repository unless the user explicitly asks in the current turn.
- Before changing files, inspect the existing repository shape and preserve its conventions.
- Prefer small, reviewable edits over broad rewrites.
- Report exact validation commands and whether they passed.
- If GitHub Actions fail, reproduce the relevant local command first, then fix the smallest repo issue that explains the failure.

## Current Product Scope

The first Registry slice provides:

- HTTP API for MCP server registration.
- Strict JSON manifest validation.
- File-backed JSON storage.
- list, inspect, create/update, and delete behavior.
- health, readiness, version, and basic metrics endpoints.

Out of scope until approved by product and architecture docs:

- authentication and authorization,
- policy enforcement,
- database-backed multi-user Registry,
- Gateway route generation,
- Kubernetes CRD synchronization,
- audit log durability,
- commercial hosted control plane behavior.

## Coding Principles

- Prefer simple, dependency-light Go code until requirements justify external
  dependencies.
- Validate all user input at API boundaries.
- Keep API responses stable and explicit.
- Use context-aware HTTP server behavior.
- Make storage testable without network access.
- Use atomic local file writes.
- Avoid global mutable state except build metadata.
- Do not introduce Gateway or Operator logic into Registry.

## Go Standards

- Run `gofmt` on changed Go files.
- Run `go test ./...`.
- Run `go vet ./...`.
- Keep command/server behavior covered by tests.
- Use `t.TempDir()` for filesystem tests.
- Avoid tests that require Docker, network, or a global home directory.

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials.
- Do not log secret values.
- Treat manifests and API requests as untrusted input.
- Use non-root containers.
- Use least-privilege Kubernetes defaults.
- Disable Kubernetes service account token automount unless needed.
- Pin Docker base images by digest for release-grade builds.
- Add OCI image labels.
- Do not claim auth, RBAC, audit, or policy controls exist until they are
  implemented and tested.

## Validation Commands

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go run . --help
```

Docker:

```bash
docker build -t kavrynt/registry:dev .
docker run --rm -p 8080:8080 kavrynt/registry:dev
```

Helm:

```bash
helm lint charts/registry
helm template registry charts/registry
```
