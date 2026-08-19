# AGENTS.md

This repository contains Kavrynt Kubernetes Operator, the Kubernetes-native
control-plane component that reconciles `MCPServer` custom resources into
Kavrynt Registry records.

Kavrynt is an umbrella for commercial infrastructure products. The current focus
is the Kavrynt MCP Control Plane for running and operating AI agents and MCP
servers in Kubernetes. The Operator is one of the four MCP Control Plane
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

- This repository started empty on `main`; create `develop` from the first reviewed MVP commit.
- The user reviews and pushes unless they explicitly ask Codex to commit or push in the current turn.

Suggested first-time branch bootstrap:

```bash
git switch main
git switch -c develop
git push -u origin develop
git switch develop
```

## Current Product Scope

The first Operator slice provides:

- `MCPServer` CRD under `kavrynt.io/v1alpha1`.
- Controller-runtime based reconciliation.
- Registry upsert on create/update.
- Registry delete on Kubernetes resource deletion.
- Finalizer for Registry cleanup.
- Status condition for Registry sync visibility.
- Docker image, GitHub Actions QA, Helm chart, raw manifests, and runbook.

Out of scope until approved by product and architecture docs:

- deploying MCP server workloads,
- managing secrets or credentials,
- mutating/admission webhooks,
- CRD version conversion,
- Gateway-specific Kubernetes APIs,
- policy enforcement,
- hosted control-plane behavior.

## Coding Principles

- Prefer controller-runtime patterns and explicit reconciliation behavior.
- Keep Kubernetes API code separate from Registry HTTP client code.
- Treat all custom resource spec fields as untrusted input.
- Do not introduce Gateway routing behavior into the Operator.
- Make reconcile logic testable with fake Kubernetes clients and fake HTTP transports.
- Avoid global mutable state except build metadata.

## Go Standards

- Run `gofmt` on changed Go files.
- Run `go test ./...`.
- Run `go vet ./...`.
- Keep controller logic covered by focused tests.
- Prefer fake clients/transports for unit tests.
- Avoid tests that require Docker, a live cluster, or a global kubeconfig unless explicitly marked as integration tests.

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials.
- Do not log secret values.
- Use non-root containers.
- Use least-privilege RBAC.
- The Operator needs a Kubernetes API token; do not disable service account token automount on the manager pod unless another auth mechanism is configured.
- Keep `readOnlyRootFilesystem: true` unless a write path is explicitly required.
- Pin Docker base images by digest for release-grade builds.
- Add OCI image labels for release-grade builds.
- Do not claim auth, RBAC isolation, mTLS, admission control, audit, or policy controls exist until implemented and tested.

## Validation Commands

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go run . --help
helm lint charts/k8s-operator
helm template k8s-operator charts/k8s-operator
kubectl kustomize config
```

Docker:

```bash
docker build -t ghcr.io/kavrynt/k8s-operator:beta-01 -t kavrynt/k8s-operator:beta-01 .
docker run --rm ghcr.io/kavrynt/k8s-operator:beta-01 --help
```
