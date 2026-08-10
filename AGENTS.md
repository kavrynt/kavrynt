# AGENTS.md

This repository is the public Kavrynt product monorepo.

Kavrynt is an MCP infrastructure control plane. The MVP contains four product
components:

- `cmd/kavryctl`: developer and operator CLI.
- `services/registry`: Registry API and MCP server metadata source of truth.
- `services/gateway`: runtime gateway for MCP traffic.
- `operator`: Kubernetes operator for `MCPServer` custom resources.

## Repository Strategy

Use this repository as the primary development location for the public MVP.
Treat the older private component repositories as pre-monorepo sources after the
monorepo is reviewed and published.

Keep each component independently buildable until there is a clear reason to
collapse the Go modules into one root module.

## Branching Strategy

Use lightweight GitFlow:

- `main`: production-ready release baseline.
- `develop`: latest integrated MVP development.
- `feature/<branch-name>`: larger or riskier feature work when isolation is
  useful.
- `release/<version>`: release stabilization when needed.
- `hotfix/<branch-name>`: urgent production fixes from `main`.

Rules:

1. Default normal MVP integration work to `develop`.
2. Do not force-push shared branches.
3. Keep changes scoped to one clear intent.
4. Run local validation before asking for review.
5. Do not commit secrets, local state, generated caches, or machine-specific
   files.
6. Do not push unless the user explicitly asks in the current turn.

## Coding Principles

- Prefer simple, dependency-light Go until requirements justify additional
  dependencies.
- Keep component boundaries clear: CLI should not own server runtime behavior,
  Registry should not proxy traffic, Gateway should not become the source of
  truth, and Operator should not own hosted product behavior.
- Validate all input at API, CLI, and Kubernetes boundaries.
- Keep behavior testable without Docker, Kubernetes, or network access where
  possible.
- Use explicit errors that give users an action they can take.
- Keep docs and runbooks aligned with behavior changes.

## Go Standards

- Run `gofmt` on changed Go files.
- Run `go test ./...` inside each changed module.
- Run `go vet ./...` inside each changed module.
- Prefer table-driven tests when multiple cases share structure.
- Use `t.TempDir()` for filesystem tests.
- Avoid tests that depend on global machine state.

Root validation:

```bash
make qa
```

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials.
- Do not log secret values.
- Treat manifests, API requests, and Kubernetes custom resources as untrusted
  input.
- Use non-root containers.
- Use least-privilege Kubernetes RBAC.
- Disable Kubernetes service account token automount unless a workload needs it.
- Pin Docker base images by digest before release-grade builds.
- Add OCI image labels before release-grade builds.
- Do not claim auth, RBAC, policy, audit, or hosted commercial controls exist
  until they are implemented and tested.

## Public Repository Hygiene

- Keep private planning documents out of this repository unless they are meant
  for public readers.
- Keep commercial implementation details out of the open-source tree until the
  product boundary is intentionally designed.
- Keep root README, component READMEs, and runbooks contributor-friendly.
- Add a license only after Kavrynt's open-source/commercial boundary is decided.
