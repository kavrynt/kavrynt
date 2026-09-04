# AGENTS.md

This is the private Kavrynt integration and release repository for the
Kubernetes-native MCP Control Plane.

## Repository Ownership

Canonical runtime source lives in separate sibling repositories:

- `../kavryctl`: developer and platform CLI.
- `../registry`: MCP server metadata API and source of truth.
- `../gateway`: MCP runtime routing and proxying.
- `../operator`: Kubernetes `MCPServer` reconciliation.

This repository owns only cross-component concerns:

- the umbrella Helm chart,
- local and CI end-to-end workflows,
- coordinated version manifests,
- trial installation and validation runbooks.

The existing `cmd/`, `services/`, and `operator/` trees are frozen legacy
snapshots. Do not implement new runtime behavior there. Port any behavior that
must be retained into the canonical sibling repository with tests, then remove
the duplicate only through a separately reviewed migration.

## Product Boundary

Kavrynt is a private commercial product. Do not describe this repository or
the runtime source as open source or source-available. Trial users consume
approved alpha or beta images, client binaries, Helm charts, and runbooks; they
do not receive source repository access by default.

## Branching

Use GitFlow:

- `main`: validated release baseline.
- `develop`: integrated development.
- `feature/<short-kebab-case-name>`: scoped feature work.
- `release/<version>`: release stabilization when required.
- `hotfix/<short-kebab-case-name>`: urgent production fixes.

Never force-push shared branches. Preserve user changes and inspect repository
status before editing. Commit or push only when the user explicitly requests
it.

## Integration Standards

- Keep runtime repositories independently buildable and releasable.
- Pin the exact component versions used by each coordinated release.
- Treat Kubernetes manifests, MCP payloads, Registry data, and Gateway traffic
  as untrusted input.
- Use non-root containers, least-privilege RBAC, and no embedded credentials.
- Do not claim authentication, tenant isolation, policy, audit, or SaaS
  controls exist until they are implemented and tested.
- Keep the Kind workflow disposable and isolated from the user's current
  Kubernetes context.
- On macOS and Apple Silicon, build images locally and load them into Kind.
- Prefer port-forwarding over NodePort for local verification.

## Required Validation

Run the canonical runtime QA:

```bash
make qa
```

Run the complete local product flow:

```bash
make e2e-kind
```

The end-to-end workflow must verify:

1. Registry, Gateway, and Operator become ready.
2. Applying an `MCPServer` produces a successful `Registered` condition.
3. Registry and Gateway expose the expected server identity.
4. A JSON-RPC `tools/list` call succeeds through Gateway.
5. Deleting the `MCPServer` removes Registry and Gateway state.
