# 0.0.1 Beta Release Runbook

Kavrynt runtime releases are built from the canonical private repositories.
The integration repository publishes only the umbrella Helm chart.

## Registry Policy

- Docker Hub hosts public beta trial images under `docker.io/kavrynt`.
- GHCR hosts the matching packages under `ghcr.io/kavrynt` and may remain
  private.
- Release workflows publish immutable version tags only.
- The Registry, Gateway, and Operator workflows publish one identical
  multi-architecture digest to both registries.
- `kavryctl` is distributed as signed GitHub Release archives for Linux,
  macOS, and Windows on AMD64 and ARM64.

## One-Time GitHub Configuration

Create a protected `release` environment in the Registry, Gateway, Operator,
and `kavryctl` repositories. Require an owner approval before deployment.

Add these environment secrets to Registry, Gateway, and Operator:

| Secret | Purpose |
| --- | --- |
| `DOCKERHUB_USERNAME` | Docker Hub organization publisher account |
| `DOCKERHUB_TOKEN` | Read/write token limited to the Kavrynt repositories |

The workflows use the built-in `GITHUB_TOKEN` for GHCR and GitHub Releases and
GitHub OIDC for keyless Cosign signing. Do not create long-lived signing keys.

Add the organization secret `KAVRYNT_REPO_TOKEN` to the integration repository.
It must have read-only Contents access to `kavryctl`, `registry`, `gateway`,
and `operator`.

Protect `develop` and `main` in every runtime repository. Require `Secure QA`
before merge, prevent force pushes, and require pull requests for `main`.

## Release Order

1. Merge each tested runtime feature branch into `develop`.
2. Confirm `Secure QA` passes on every `develop` branch.
3. Merge `develop` into `main` and confirm `Secure QA` passes again.
4. Create the annotated tag `v0.0.1-beta` on each canonical runtime `main`.
5. Wait for all three image workflows and the `kavryctl` binary workflow.
6. Verify both registries expose AMD64 and ARM64 manifests.
7. Create `v0.0.1-beta` on the integration repository `main` to publish the
   umbrella chart from the exact component tags.
8. Run the Kind workflow against the released version before announcing it.

## Verification

```bash
docker buildx imagetools inspect docker.io/kavrynt/registry:0.0.1-beta
docker buildx imagetools inspect docker.io/kavrynt/gateway:0.0.1-beta
docker buildx imagetools inspect docker.io/kavrynt/operator:0.0.1-beta

cosign verify \
  --certificate-identity-regexp='https://github.com/kavrynt/.+/.github/workflows/release.yml@refs/tags/v0.0.1-beta' \
  --certificate-oidc-issuer='https://token.actions.githubusercontent.com' \
  docker.io/kavrynt/registry:0.0.1-beta
```

Download `kavryctl` from the private GitHub Release and verify `SHA256SUMS`
with its `.sig` and `.pem` files before installation.

## Failure Rule

Do not move or reuse `v0.0.1-beta`. If any artifact is incorrect, fix the
source and publish the next prerelease tag, for example `v0.0.1-beta.1`.
