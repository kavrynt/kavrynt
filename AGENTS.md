# AGENTS.md

This is the private Kavrynt runtime repository for the Kubernetes-native MCP
Control Plane. It holds all customer-installed components and their release
pipeline ([ADR-0001](docs/ADR-0001-Runtime-Monorepo.md)).

## Repository Ownership

| Path | Owns |
| --- | --- |
| `cmd/kavryctl`, `internal/kavryctl` | Developer and platform CLI |
| `cmd/gateway`, `internal/gateway` | MCP runtime routing and (future) enforcement |
| `cmd/operator`, `internal/operator`, `api/v1alpha1`, `config/` | `MCPServer` CRD and reconciliation |
| `charts/kavrynt` | The Helm chart with vendored component subcharts |
| `build/Dockerfile` | All runtime images (`--target gateway`, `operator`) |
| `test/e2e`, `scripts/` | Kind end-to-end tests, installers, release checks |

The hosted control plane lives in `kavrynt-cloud`; Azure infrastructure lives
in `kavrynt-platform`. The former `kavryctl`, `registry`, `gateway`, and
`operator` repositories are archived and must not receive new changes.

## Architecture Decisions In Force

- [ADR-0001](docs/ADR-0001-Runtime-Monorepo.md): one module, one version, one chart.
- [ADR-0002](docs/ADR-0002-Remove-In-Cluster-Registry.md): the `MCPServer` API
  replaces the in-cluster Registry.
- [ADR-0003](docs/ADR-0003-Runtime-Authorization.md): Gateway authorization
  design (not implemented yet).
- Identity planes and threat model: `kavrynt-cloud/docs/ADR-0006-Identity-Planes.md`
  and `kavrynt-cloud/docs/THREAT-0001-System-Threat-Model.md`.

## Product Boundary

Kavrynt is a private commercial product. Do not describe this repository or
the runtime source as open source or source-available. Trial users consume
approved alpha or beta images, client binaries, Helm charts, and runbooks; they
do not receive source repository access by default. Do not add source-code
GitHub links to customer-facing docs.

## Branching

Use GitFlow:

- `main`: validated release baseline. Release tags are cut from `main`.
- `develop`: integrated development and the default branch.
- `feature/<short-kebab-case-name>`: one logical change, merged back into
  `develop` after `make qa` and `make e2e-kind` pass.
- `release/<version>`: release stabilization when required.
- `hotfix/<short-kebab-case-name>`: urgent production fixes.

Never force-push shared branches. Preserve user changes and inspect repository
status before editing. Commit, push, merge, or publish only when the owner
explicitly requests it.

## Engineering Standards

- Keep components separated by package: Gateway must not contain
  reconciliation logic; the Operator must not proxy traffic; `kavryctl` must
  not import server internals other than shared API types.
- Shared Kubernetes API types live only in `api/v1alpha1`.
- Prefer simple, dependency-light Go. New dependencies must pass govulncheck
  and Trivy gates.
- Treat Kubernetes manifests, MCP payloads, tool metadata, and Gateway traffic
  as untrusted input.
- Keep behaviour testable without network access; prefer fake
  `http.RoundTripper`s and `t.TempDir()`.
- Use explicit HTTP status codes and stable JSON responses.

## Security Standards

- Never commit secrets, tokens, kubeconfigs, private keys, or credentials, and
  never log secret values.
- Non-root, distroless images pinned by digest, with OCI labels.
- Least-privilege RBAC; disable service account token automount unless needed;
  `readOnlyRootFilesystem: true` unless a write path is required.
- Do not claim authentication, tenant isolation, policy, audit, or SaaS
  controls exist until they are implemented and tested.
- Images are published only by `.github/workflows/release.yml` after all gates
  pass. No mutable tags such as `latest`.

## Required Validation

```bash
make qa        # fmt, race tests, vet, staticcheck, gosec, govulncheck, Helm, release contract
make e2e-kind     # disposable Kind cluster, full product loop
```

The end-to-end workflow must verify:

1. Gateway and Operator become ready.
2. Applying an `MCPServer` produces a `Ready` condition.
3. The CRD rejects unsafe endpoints at admission.
4. Gateway RBAC is read-only on `MCPServer` and has no Secret access.
5. Gateway and `kavryctl list` expose the expected route.
6. A JSON-RPC `tools/list` call succeeds through Gateway.
7. `kavryctl register`, `inspect`, and `unregister` work against the cluster.
8. Deleting an `MCPServer` removes its Gateway route.

Keep the Kind workflow disposable and isolated from the user's current
Kubernetes context. On macOS and Apple Silicon, build images locally and load
them into Kind. Prefer port-forwarding over NodePort for local verification.
Do not modify or delete Kind clusters this workflow did not create.
