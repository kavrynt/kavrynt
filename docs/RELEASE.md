# Release Runbook

One `vX.Y.Z-beta.N` tag on `main` runs `.github/workflows/release.yml`, which:

1. re-runs every quality gate (`make qa`);
2. builds, Trivy-scans, publishes, and Cosign-signs `gateway` and `operator`
   images on GHCR, then mirrors the same digests to Docker Hub and signs them;
3. builds `kavryctl` for Linux, macOS, and Windows (amd64, arm64), signs
   `SHA256SUMS`, and creates a GitHub prerelease in this repository;
4. pushes the chart to `oci://ghcr.io/kavrynt/charts/kavrynt` and mirrors it
   to `oci://registry-1.docker.io/kavrynt/kavrynt`;
5. installs the published Docker Hub chart and images in Kind and runs the
   product loop (`make e2e-release`).

Nothing is published if step 1 fails. Images are published only after their
Trivy scan passes.

## One-Time Setup

| Item | Where | Why |
| --- | --- | --- |
| `release` environment | Repository settings → Environments | Referenced by the publish jobs |
| `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN` | Repository or `release` environment secrets | Docker Hub mirror for images and chart. Use an access token scoped to the `kavrynt` namespace with read/write |
| Actions write access for `kavrynt/kavrynt` | GHCR package settings for `gateway`, `operator`, `charts/kavrynt` → Manage Actions access | Packages created by the archived component repositories are linked to them |
| Public Docker Hub repositories `kavrynt/gateway`, `kavrynt/operator`, `kavrynt/kavrynt` | Docker Hub | Trial users pull anonymously |

On the GitHub Free plan, environment secrets and required reviewers are not
available for private repositories. Use repository secrets until the
organization moves to GitHub Team, then move the secrets into the `release`
environment and require an owner approval.

## Release Steps

```bash
# 1. Confirm develop is green.
gh run list -R kavrynt/kavrynt --branch develop --workflow qa.yml --limit 1

# 2. Promote develop to main.
git switch main && git pull --ff-only
git merge --no-ff develop -m "Release 0.0.2-beta.1"
git push origin main

# 3. Tag and push (the version must equal charts/kavrynt/Chart.yaml).
git tag -a v0.0.2-beta.1 -m "Kavrynt 0.0.2-beta.1"
git push origin v0.0.2-beta.1

# 4. Watch the workflow.
gh run watch -R kavrynt/kavrynt "$(gh run list -R kavrynt/kavrynt --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
```

## Verify

Use Cosign 3.0 or newer: the workflow signs with Cosign 3, and Cosign 2
reports "no signatures found".

```bash
VERSION=0.0.2-beta.1
for image in gateway operator; do
  cosign verify \
    --certificate-identity "https://github.com/kavrynt/kavrynt/.github/workflows/release.yml@refs/tags/v${VERSION}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "docker.io/kavrynt/${image}:${VERSION}"
done
helm show chart oci://registry-1.docker.io/kavrynt/kavrynt --version "$VERSION"
```

Only after verification: merge the prepared `product-docs` and `website`
branches that describe the new version.

## If The Workflow Fails

- Before any publish step: fix on `develop`, promote again, delete and recreate
  the tag (`git push --delete origin vX` then re-tag). Nothing was published.
- After images were published: never overwrite a published tag. Fix forward
  with the next prerelease number (`0.0.2-beta.2`).
