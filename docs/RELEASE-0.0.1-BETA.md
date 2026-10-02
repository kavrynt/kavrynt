# 0.0.1 Beta.1 Release Runbook

> [!NOTE]
> Historical. This runbook describes releases from the former per-component
> repositories. From the runtime monorepo onwards, one `vX.Y.Z-beta.N` tag on
> `main` in this repository runs `.github/workflows/release.yml`, which
> publishes all images, `kavryctl`, and the chart. One-time setup moves here:
> a protected `release` environment with `DOCKERHUB_USERNAME` and
> `DOCKERHUB_TOKEN`, and Actions write access on each GHCR package.

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

1. Run `make qa` and `make e2e-kind` from the integration repository against
   the four canonical sibling repositories.
2. Merge each tested runtime feature branch into `develop`.
3. Confirm `Secure QA` passes on every `develop` branch.
4. Merge `develop` into `main` and confirm `Secure QA` passes again.
5. Create the annotated tag `v0.0.1-beta.1` on each canonical runtime `main`.
6. Wait for all three image workflows and the `kavryctl` binary workflow.
7. Verify both registries expose AMD64 and ARM64 manifests and that Cosign
   verification succeeds.
8. Create `v0.0.1-beta.1` on the integration repository `main`. Its release
   workflow verifies the component images, publishes the umbrella chart, and
   runs the published-artifact Kind test.
9. Confirm the release workflow ends with the following message before
   announcing the beta:

   ```text
   published release 0.0.1-beta.1 end-to-end workflow passed
   ```

## Verification

```bash
docker buildx imagetools inspect docker.io/kavrynt/registry:0.0.1-beta.1
docker buildx imagetools inspect docker.io/kavrynt/gateway:0.0.1-beta.1
docker buildx imagetools inspect docker.io/kavrynt/operator:0.0.1-beta.1

cosign verify \
  --certificate-identity-regexp='https://github.com/kavrynt/.+/.github/workflows/release.yml@refs/tags/v0.0.1-beta.1' \
  --certificate-oidc-issuer='https://token.actions.githubusercontent.com' \
  docker.io/kavrynt/registry:0.0.1-beta.1
```

Download `kavryctl` from the private GitHub Release. Assets follow the naming
contract `kavryctl_<version>_<os>_<arch>.<archive>`. Verify `SHA256SUMS` with
its `SHA256SUMS.sigstore.json` Sigstore bundle before installation.

After publication, the same released-artifact validation can be repeated
locally:

```bash
make e2e-release RELEASE_VERSION=0.0.1-beta.1
```

This pulls `registry`, `gateway`, and `operator` from Docker Hub and installs
the matching OCI chart from GHCR. It does not build runtime components from
source.

## Failure Rule

Do not move or reuse `v0.0.1-beta` or `v0.0.1-beta.1`. If any beta.1 artifact
is incorrect, fix the source and publish `v0.0.1-beta.2`.
