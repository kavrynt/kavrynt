---
name: cut-release
description: Prepare and cut a Kavrynt runtime prerelease (vX.Y.Z-alpha.N or -beta.N) - bump the version everywhere the release contract checks, run QA, promote develop to main, tag, watch the Release workflow, and verify signatures.
argument-hint: <version, e.g. 0.0.3-beta.1>
disable-model-invocation: true
---

# Cut a runtime release

Target version: `$ARGUMENTS` (no leading `v`). If it is empty, ask for it. It must match
`X.Y.Z-(alpha|beta)[.N]`; the Release workflow rejects anything else.

Full background: `docs/RELEASE.md`. Never overwrite a published tag; if images were already
published for a version, fix forward with the next prerelease number.

## 1. Preconditions

```bash
git status --short                      # must be clean
git switch develop && git pull --ff-only
gh run list -R kavrynt/kavrynt --branch develop --workflow qa.yml --limit 1
```

Stop if QA on `develop` is not green, and report the failing job.

## 2. Bump the version on a release branch

`git switch -c release/<version>`, then set the new version in every place
`scripts/verify-release-contract.sh` checks:

| File | What |
| --- | --- |
| `charts/kavrynt/Chart.yaml` | `version`, `appVersion`, dependency versions, `artifacthub.io/images` tags, and `artifacthub.io/prerelease` (`"false"` only for a stable version) |
| `charts/kavrynt/charts/gateway/Chart.yaml` | `version` and `appVersion` |
| `charts/kavrynt/charts/k8s-operator/Chart.yaml` | `version` and `appVersion` |
| `charts/kavrynt/values.yaml` | both image `tag:` lines |
| `charts/kavrynt/charts/{gateway,k8s-operator}/values.yaml` | image `tag:` |
| `config/manager/deployment.yaml` | operator image tag |
| `scripts/install.sh`, `scripts/install.ps1` | `v<version>` |
| `README.md`, `charts/kavrynt/README.md` | install examples (`KAVRYNT_VERSION`, `--version`) |
| `Makefile`, `scripts/e2e-kind.sh` | `LOCAL_VERSION` default (`<version>-local`) |

Find stragglers with `grep -rn "<old version>" --exclude-dir=.git --exclude-dir=.cache .`.
Leave historical mentions in `docs/ADR-*.md` and `docs/RELEASE.md` examples alone.
If the CRD changed since the last release, remind the owner that `helm upgrade` does not
update CRDs (see `charts/kavrynt/README.md`).

## 3. Validate

```bash
make qa
make e2e-kind
```

Both must pass. Show the diff summary and the results to the owner.

## 4. Promote and tag (only after the owner says go)

Each command below changes GitHub. Ask before running them, and run them one step at a time.

```bash
git commit -am "Release <version>"
git switch develop && git merge --no-ff release/<version> && git push origin develop
git switch main && git pull --ff-only
git merge --no-ff develop -m "Release <version>" && git push origin main
git tag -a v<version> -m "Kavrynt <version>" && git push origin v<version>
git branch -d release/<version>
```

## 5. Watch and verify

```bash
gh run watch -R kavrynt/kavrynt "$(gh run list -R kavrynt/kavrynt --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
```

Then verify with Cosign 3+ (Cosign 2 reports "no signatures found"):

```bash
VERSION=<version>
for image in gateway operator; do
  cosign verify \
    --certificate-identity "https://github.com/kavrynt/kavrynt/.github/workflows/release.yml@refs/tags/v${VERSION}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "docker.io/kavrynt/${image}:${VERSION}"
done
helm show chart oci://registry-1.docker.io/kavrynt/kavrynt --version "$VERSION"
```

## If the workflow fails

- Before any publish step: fix on `develop`, promote again, then delete and recreate the tag
  (`git push --delete origin v<version>`). Ask first.
- After images were published: do not reuse the version; start again with the next number.

Finish by reporting: version, run URL, verification output, and the follow-ups from
`docs/RELEASE.md` (product-docs and website branches describing the version).
