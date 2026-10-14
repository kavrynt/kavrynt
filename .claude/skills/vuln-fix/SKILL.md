---
name: vuln-fix
description: Fix Go vulnerabilities reported by govulncheck, Trivy, or Dependabot in the Kavrynt runtime - find the fixed module or Go version, bump it, update the build image and CI, and prove the gates pass. Use when QA fails on govulncheck or Trivy, or when asked to fix a GO-YYYY-NNNN or CVE.
argument-hint: "[GO-ID or CVE, optional]"
---

# Fix a Go vulnerability

Input: `$ARGUMENTS` (optional advisory IDs). Without them, find the findings first.

## 1. Find what is vulnerable

```bash
git switch develop && git pull --ff-only
make vulncheck            # govulncheck: reachable findings only
gh run list -R kavrynt/kavrynt --branch develop --workflow qa.yml --limit 3
```

For a failed CI run, read the failing step: `gh run view <id> --log-failed`. For each
finding, note the advisory ID, the module, the fixed version, and whether the fix is in a
module (`golang.org/x/net`) or the Go standard library (`net/http`, `crypto/...`).

## 2. Fix it

On `feature/fix-<short-advisory-or-module>`:

- **Module:** `go get <module>@<fixed version> && go mod tidy`. Note the indirect modules the
  bump pulls in.
- **Standard library:** the fix needs a newer Go patch release.
  - `go.mod` keeps `go 1.26.0`; the toolchain comes from the build image and CI.
  - Update `ARG GO_IMAGE=golang:1.26-alpine@sha256:…` in `build/Dockerfile` to the digest of
    the patched release: `docker buildx imagetools inspect golang:1.26-alpine`.
  - CI uses `actions/setup-go` with `go-version: "1.26.x"` and `check-latest: true`. Keep
    `check-latest`, so runners take the newest patch.
- **Trivy-only findings in images:** usually the base image. Update `ARG DISTROLESS_IMAGE`'s digest.

## 3. Prove it

```bash
make vulncheck
make qa
make docker-build    # then: trivy image kavrynt/gateway:<LOCAL_VERSION> (and operator) if Trivy is installed
```

## 4. Report and commit (when asked)

Commit message style, matching the history:

```text
Fix Go vulnerabilities GO-2026-NNNN, GO-2026-MMMM

- <module> vA -> vB (GO-..., CVE-..., HIGH). It brings <indirect bumps>.
- Go 1.26.N for the <package> fixes (GO-...): the build image digest moves to
  golang:1.26-alpine at 1.26.N.
```

Merge back to `develop` through GitFlow once QA passes. Commit, push, or merge only when the
owner asks.
