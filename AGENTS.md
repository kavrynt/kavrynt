# AGENTS.md

This repository contains Kavrynt Gateway, the MVP data-plane entry point for
routing client MCP traffic to HTTP MCP servers registered in Kavrynt Registry.

Kavrynt is an open-source AI infrastructure platform for running and operating
AI agents and MCP servers in production. Gateway is one of the four MVP
components:

- kavryctl
- Registry
- Gateway
- Kubernetes Operator

## Branching Strategy

Use GitFlow-compatible branch roles.

- `main`: production-ready code only.
- `develop`: latest integrated development code.
- `feature/<branch-name>`: isolated work when specifically useful.
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

- If the remote only has `main`, create `develop` from the current reviewed commit before ongoing MVP work continues.
- The user reviews and pushes unless they explicitly ask Codex to commit or push in the current turn.

Suggested first-time branch bootstrap:

```bash
git switch main
git switch -c develop
git push -u origin develop
git switch develop
```

## Current Product Scope

The first Gateway slice provides:

- HTTP API for health, readiness, version, metrics, and route listing.
- Registry client for `GET /v1/servers`.
- Periodic Registry sync into an in-memory route table.
- HTTP MCP request proxying through `/mcp/<server-name>`.
- Docker image, GitHub Actions QA, Helm chart, and local runbook.

Out of scope until approved by product and architecture docs:

- authentication and authorization,
- tenant isolation,
- policy enforcement,
- stdio MCP process management,
- Gateway route persistence,
- Operator-managed dynamic configuration,
- commercial hosted control plane behavior.

## Coding Principles

- Prefer simple, dependency-light Go code until requirements justify external dependencies.
- Treat Registry data and inbound client traffic as untrusted input.
- Keep Gateway routing separate from Registry storage and Operator reconciliation.
- Use explicit HTTP status codes and stable JSON responses.
- Keep route table behavior testable without network access.
- Avoid global mutable state except build metadata.
- Do not introduce Kubernetes reconciliation logic into Gateway.

## Go Standards

- Run `gofmt` on changed Go files.
- Run `go test ./...`.
- Run `go vet ./...`.
- Use `t.TempDir()` for filesystem tests when needed.
- Prefer fake `http.RoundTripper` tests over tests that require real network listeners.
- Keep command/server behavior covered by focused tests.

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials.
- Do not log secret values.
- Use non-root containers.
- Use least-privilege Kubernetes defaults.
- Disable Kubernetes service account token automount unless needed.
- Keep `readOnlyRootFilesystem: true` unless a write path is explicitly required.
- Pin Docker base images by digest for release-grade builds.
- Add OCI image labels for release-grade builds.
- Do not claim auth, RBAC, mTLS, audit, or policy controls exist until implemented and tested.

## Validation Commands

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go run . --help
helm lint charts/gateway
helm template gateway charts/gateway
```

Docker:

```bash
docker build -t kavrynt/gateway:dev .
docker run --rm -p 8080:8080 \
  -e KAVRYNT_REGISTRY_URL=http://host.docker.internal:8081 \
  kavrynt/gateway:dev
```
