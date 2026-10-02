# AGENTS.md

This repository contains `kavryctl`, the Kavrynt command-line interface.

Kavrynt is a private commercial platform for running and operating AI agents
and MCP servers in production. The first `kavryctl` slice manages
MCP server manifests with both local file-backed registration and remote
Kavrynt Registry API registration. Gateway, Kubernetes Operator,
authentication, authorization, and policy enforcement are future work unless
an approved design says otherwise. Do not describe this repository or the
Kavrynt runtime as open source.

## Branching Strategy

Use a lightweight GitFlow model while Kavrynt is in active MVP development.

- `main`: production-ready code only.
- `develop`: latest integrated development code.
- `feature/<branch-name>`: optional branch for larger, risky, or explicitly
  requested feature work.
- `release/<version>`: release stabilization when needed.
- `hotfix/<branch-name>`: urgent production fixes from `main`.

Rules:

1. Default to doing iterative MVP work directly on `develop`.
2. Create `feature/<short-kebab-case-name>` only when the user asks, when the
   work is high-risk, or when parallel review is useful.
3. Keep each change focused even when working directly on `develop`.
4. Run local validation before pushing.
5. Merge or fast-forward tested feature branches back into `develop` when
   feature branches are used.
6. Move `main` forward only for production-ready release baselines.
7. Do not force-push shared branches.
8. Do not commit secrets, local state, build output, or machine-specific files.

## Change Workflow

For every code or infrastructure change:

1. Inspect the current repo state.
2. Confirm the branch is `develop` for normal MVP work, or a deliberate
   `feature/*` branch for larger/riskier work.
3. Keep edits scoped to the requested change.
4. Update tests and documentation when behavior changes.
5. Run formatting, tests, and relevant smoke checks.
6. Review `git status --short` and `git diff --stat`.
7. Do not commit unless explicitly requested.

## Coding Principles

- Prefer simple, dependency-light code until requirements justify new
  dependencies.
- Keep packages small and purpose-specific.
- Make behavior testable without network access.
- Use explicit errors with enough context for users to act.
- Validate user input at the boundary.
- Keep CLI output stable and predictable.
- Keep local and remote Registry workflows behaviorally aligned.
- Avoid global mutable state except for constants.
- Prefer standard library features when they are sufficient.
- Do not introduce Kubernetes, Gateway, auth, or policy logic until the
  corresponding product and architecture docs are approved.

## Go Standards

- Run `gofmt` on changed Go files.
- Run `go test ./...` before handing off.
- Run `go vet ./...` before handing off.
- Keep exported identifiers documented when they become public API.
- Keep command parsing deterministic and covered by tests.
- Prefer table-driven tests when multiple cases share structure.
- Use `t.TempDir()` for filesystem tests.
- Avoid tests that depend on the user's machine, network, Docker daemon, or
  global home directory.

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials.
- Do not log secret values.
- Treat manifest content as untrusted user input.
- Validate file-backed registry content before relying on it.
- Use least-privilege container defaults.
- Use non-root containers.
- Keep Docker images minimal.
- Pin Docker base images by digest for release-grade builds.
- Add OCI image labels to built images.
- Do not claim authentication, authorization, audit, signing, or policy
  controls exist until they are implemented and tested.
- Add security review documentation before adding auth, policy, Gateway, or
  Kubernetes deployment behavior.

## Current Validation Commands

Use a local Go build cache if the environment cannot write to the default Go
cache:

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go run . version
GOCACHE="$PWD/.cache/go-build" go run . validate examples/mcp-server.json
```

Remote Registry smoke, when the Registry service is running:

```bash
KAVRYNT_REGISTRY_URL=http://localhost:8080 GOCACHE="$PWD/.cache/go-build" go run . register examples/mcp-server.json
KAVRYNT_REGISTRY_URL=http://localhost:8080 GOCACHE="$PWD/.cache/go-build" go run . list
KAVRYNT_REGISTRY_URL=http://localhost:8080 GOCACHE="$PWD/.cache/go-build" go run . inspect example-mcp-server
KAVRYNT_REGISTRY_URL=http://localhost:8080 GOCACHE="$PWD/.cache/go-build" go run . unregister example-mcp-server
```

For Docker validation:

```bash
docker build -t kavrynt/kavryctl:dev .
docker run --rm kavrynt/kavryctl:dev version
```

For Helm validation:

```bash
helm lint charts/kavryctl
helm template kavryctl charts/kavryctl
```

## Documentation Expectations

When behavior changes, update the relevant README or docs.

Implementation must stay aligned with the product documentation in
`product-docs`, especially:

- `PRD-0001-Kavrynt-MVP.md`
- `RFC-0002-MVP-System-Architecture.md`
- future CLI API/contract documents

Draft product documents are not implementation approval for unrelated scope.
