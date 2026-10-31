# Contributing to Kavrynt

Thank you for your interest in contributing! This guide explains how to set
up your environment, make changes, test them, and submit them for review. It
is written for first-time contributors and regulars alike.

If anything here is unclear or out of date, please open an issue or a pull
request to improve it.

---

## Table of Contents

- [Getting Started](#getting-started)
- [Development Workflow](#development-workflow)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Additional Information](#additional-information)

---

## Getting Started

### Repository overview

This repository holds the Kavrynt runtime: the Kubernetes-native MCP control
plane that users install in their own clusters. Every component builds from
one Go module and ships under one version in one Helm chart
([ADR-0001](docs/ADR-0001-Runtime-Monorepo.md)).

| Component | Path | Description |
| --- | --- | --- |
| `kavryctl` | `cmd/kavryctl`, `internal/kavryctl` | CLI to validate, register, list, inspect, and remove `MCPServer` resources |
| Gateway | `cmd/gateway`, `internal/gateway` | Routes MCP traffic to `Ready` servers and serves the web console |
| Operator | `cmd/operator`, `internal/operator` | Validates `MCPServer` resources, sets conditions, discovers tools |
| `MCPServer` API | `api/v1alpha1`, `config/` | CRD Go types, CRD schema, and RBAC manifests |
| Helm chart | `charts/kavrynt` | Umbrella chart with vendored `gateway` and `k8s-operator` subcharts |
| Specs | `specs/` | Feature specifications with requirement IDs |

### Prerequisites and required tools

| Tool | Minimum version | Purpose |
| --- | --- | --- |
| [Go](https://go.dev/doc/install) | 1.26 | Building and unit testing |
| [Node.js](https://nodejs.org/) | 22 | Web console unit tests (`make console-test`); no npm packages are installed |
| [Docker](https://docs.docker.com/get-docker/) | any recent | Building runtime images |
| [Helm](https://helm.sh/docs/intro/install/) | 3.x or 4.x | Chart linting, templating, and installs |
| [helm-unittest](https://github.com/helm-unittest/helm-unittest) plugin | pinned in the Makefile | Chart unit tests; install with `make helm-plugins` |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | any recent | Talking to clusters |
| [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | any recent | Disposable clusters for end-to-end tests |
| `curl`, `jq`, `git`, GNU or BSD `grep`/`sed` | any recent | Used by the end-to-end scripts |

`staticcheck`, `gosec`, and `govulncheck` need no installation: `make qa` runs
pinned versions with `go run`.

### Cloning the repository

Fork the repository on GitHub, then clone your fork:

```bash
git clone https://github.com/<your-username>/kavrynt.git
cd kavrynt
git remote add upstream https://github.com/kavrynt/kavrynt.git
```

Keep your fork in sync with the default branch, `develop`:

```bash
git fetch upstream
git checkout develop
git merge --ff-only upstream/develop
```

### Setting up a local development environment

Confirm your toolchain works by building and running the unit tests:

```bash
make help       # list every target
make build      # bin/kavryctl, bin/gateway, bin/operator
make test       # go test -race ./...
```

To see the whole product running, `make demo-up` creates a disposable Kind
cluster with Kavrynt and two demo MCP servers
([docs/DEMO.md](docs/DEMO.md)). Kavrynt's Kind workflows create their own
clusters and never touch your current Kubernetes context.

---

## Development Workflow

### Before you start

- Search [issues](https://github.com/kavrynt/kavrynt/issues) first.
- For anything larger than a bug fix, open an issue describing the problem
  and the proposed approach before writing code.
- **User-visible changes need a spec.** A change to the `MCPServer` API,
  Gateway endpoints or security behavior, the console, `kavryctl` commands,
  Helm values, or the Cloud connector starts as a reviewed specification in
  `specs/`. See [Specs](#writing-a-spec) below.
- Changes to the architecture or security model also need an Architecture
  Decision Record in `docs/` (see the existing `ADR-*.md`).
- Report security vulnerabilities privately; see [SECURITY.md](SECURITY.md).

### Creating a feature branch

The project uses GitFlow. Branch from the latest `develop`:

```bash
git checkout develop
git pull --ff-only upstream develop
git checkout -b feature/my-change
```

Name branches `feature/<short-kebab-case-name>` or
`fix/<short-kebab-case-name>`, for example `feature/console-tool-catalog` or
`fix/gateway-origin-check`. `main` holds released versions only.

### Writing a spec

Kavrynt uses spec-driven development ([specs/README.md](specs/README.md)):

```bash
make spec-new NAME=console-tool-catalog   # creates specs/NNNN-console-tool-catalog/spec.md
```

1. Open a pull request with `spec.md` in status `Draft`. Review covers
   behavior only: requirements, acceptance criteria, non-goals, security.
2. When a maintainer approves, the status becomes `Accepted` and you add
   `plan.md` (and `tasks.md` for multi-PR work).
3. Implement. Each test cites the requirements it verifies, for example
   `// Verifies SPEC-0007-R02.`
4. The final pull request sets the status to `Implemented` and updates the
   docs. `make spec-check` fails if any requirement has no test.

### Where changes go

| You are changing | Also update |
| --- | --- |
| `MCPServer` Go types in `api/v1alpha1` | Both CRD copies, kept identical: `config/crd/bases/kavrynt.io_mcpservers.yaml` and `charts/kavrynt/charts/k8s-operator/files/kavrynt.io_mcpservers.yaml` (hand-written, no controller-gen) |
| RBAC | `config/rbac` and the chart templates together |
| Helm values | The subchart `values.yaml` and its hand-written `values.schema.json` (unknown keys are rejected, so a new value without a schema entry fails lint), the umbrella `charts/kavrynt/values.yaml` if exposed there, the values tables in [charts/kavrynt/README.md](charts/kavrynt/README.md), and a test in `charts/kavrynt/tests/` |
| Chart templates | A helm-unittest case in `charts/kavrynt/tests/` for the behavior you change |
| Gateway or Operator flags | The component doc in `docs/components/` |
| The console (`internal/gateway/console/static`) | Plain HTML, CSS, and JavaScript only: no build step, no external assets, untrusted data through `textContent` only. Classic scripts, not ES modules: module scripts send an `Origin` header that the Gateway rejects outside `--allowed-origins` |
| Sample manifests in `examples/kavryctl` | They must pass `kavryctl validate` (CI checks every file) |
| User-visible behavior | README, `docs/components/`, and `ROADMAP.md` once the spec is `Implemented` |

Do not bump versions. The single release version is set by maintainers at
release time (see [Release process](#release-process)).

### Coding and documentation conventions

- Keep components separated: the Gateway contains no reconciliation logic, the
  Operator never proxies traffic (tool discovery is its own MCP client
  session), and `kavryctl` imports only shared API types. Limits shared by
  the Gateway and the Operator live in `internal/mcpmeta`; egress policy lives
  in `internal/egress`.
- Prefer the standard library and simple Go. New dependencies must pass
  govulncheck and Trivy.
- Treat Kubernetes manifests, MCP payloads, tool metadata, and Gateway
  traffic as untrusted input.
- Use explicit HTTP status codes and stable JSON responses.
- Never commit secrets, tokens, kubeconfigs, or private keys, and never log
  secret values.
- Write docs only for what the code does today. Do not describe unimplemented
  features (client authentication, authorization, audit, high availability)
  as available.
- This repository is public: keep internal infrastructure, account details,
  and customer names out of code, docs, and commit messages.

---

## Testing

Run everything from the repository root. CI runs the same checks, so running
them first saves a review cycle.

### Running unit tests

```bash
make test                                   # whole module, race detector on
go test -race ./internal/gateway/...        # one component
go test -race -run TestConsole ./internal/gateway/api
```

Tests must run without network access: use fake `http.RoundTripper`s and
`t.TempDir()`.

The console's logic (`stats.js`, `state.js`, `format.js` in
`internal/gateway/console/static`) has no DOM access and is tested with
Node's built-in test runner:

```bash
make console-test                           # node --test 'test/console/*.test.mjs'
```

### Running lint and security checks

```bash
make fmt-check    # gofmt
make vet          # go vet
make lint         # staticcheck
make sast         # gosec
make vulncheck    # govulncheck
```

### Validating the Helm chart and release contract

```bash
make helm-lint          # helm lint charts/kavrynt, including values schemas
make helm-template      # render the chart
make helm-plugins       # once: install the pinned helm-unittest plugin
make helm-unittest      # chart unit tests in charts/kavrynt/tests/
make release-contract   # one version in every chart, values file, and installer
```

Chart unit tests render the umbrella chart, so set values the way users do
(`gateway.console.enabled`). Template paths use the subchart names under
`charts/`, for example `charts/gateway/templates/rbac.yaml` and
`charts/operator/templates/deployment.yaml`.

### Checking specs

```bash
make spec-check   # specs are well formed; Implemented requirements are cited by tests
```

### Running the end-to-end tests

```bash
make e2e-kind
```

This builds local images, loads them into a disposable Kind cluster, installs
the chart, and verifies the full product loop: readiness, admission,
RBAC, tool discovery, routing, `kavryctl`, the console, and egress
protections. Failure diagnosis: [docs/E2E.md](docs/E2E.md).

### Verifying changes locally before submission

```bash
make qa         # every gate above except e2e
make e2e-kind   # required unless the change cannot affect the product loop
```

---

## Submitting Changes

### Commit message guidelines

- Write a clear, imperative subject, for example "Add a tool catalog to the
  console", not "added catalog".
- Keep the subject under about 72 characters; use the body to explain *why*
  when it is not obvious.
- Reference the spec or issue in the body when there is one
  (`Implements SPEC-0002-R04`, `Fixes #123`).
- Group related changes into logical commits.

### DCO sign-off requirements

Every commit must be signed off under the
[Developer Certificate of Origin 1.1](https://developercertificate.org/),
certifying that you wrote the change or otherwise have the right to submit it
under the project's license:

```bash
git commit -s -m "Add a tool catalog to the console"
```

This appends a trailer to the commit message:

```text
Signed-off-by: Your Name <your.email@example.com>
```

Use your real name. The name and email come from your Git configuration:

```bash
git config user.name "Your Name"
git config user.email "your.email@example.com"
```

If you forgot to sign off the last commit:

```bash
git commit --amend -s --no-edit
```

To sign off every commit on your branch:

```bash
git rebase --signoff develop
```

### Licensing of contributions

Kavrynt is licensed under the [Apache License 2.0](LICENSE). By contributing,
you agree that your contribution is licensed under the same license, as
described in section 5 of the license.

### Creating a pull request

1. Push your branch to your fork:

   ```bash
   git push origin feature/my-change
   ```

2. Open a pull request against `develop` in `kavrynt/kavrynt`.
3. Explain **what** changed and **why**, link the issue (`Fixes #123`) and the
   spec (`specs/NNNN-name`) if there is one.
4. Complete the checklist in the pull request template.

CI runs Go QA and SAST, a Trivy source scan, the Helm and release contract
checks, spec checks, multi-architecture image builds with Trivy, and the Kind
end-to-end job. All checks must be green.

A pull request can be merged when:

1. CI is green, including the Kind end-to-end job;
2. CodeRabbit's review of the latest commit has finished, and every
   finding is verified, fixed when it holds, and replied to on its thread
   (with the fixing commit, or why it does not apply); fixes start a new
   review, so repeat until it raises nothing new;
3. new behavior has tests that run without network access, citing their
   spec requirements;
4. docs describe only what the code actually does;
5. every commit is signed off;
6. a maintainer has approved it.

### Stacking large changes

Split large or multi-part work into a sequence of small pull requests, each
independently reviewable and each leaving `develop` green. A spec's
`tasks.md` is a good place to plan the sequence.

### Addressing review feedback

- Push follow-up commits to the same branch; the pull request updates
  automatically.
- CodeRabbit reviews pull requests and their new commits automatically,
  except drafts, Dependabot's, and the paths and base branches
  `.coderabbit.yaml` excludes. Check each of its findings against the code,
  fix the ones that hold, and reply on every thread with the fixing commit
  or why the finding does not apply.
- Prefer new commits over force-pushes during active review so reviewers can
  see what changed. Never force-push `develop` or `main`.
- Sign off new commits too (`git commit -s`).
- Once approved, a maintainer merges the pull request.

---

## Additional Information

### Repository structure overview

```text
.
├── api/v1alpha1/          # MCPServer CRD Go types (shared API)
├── build/Dockerfile       # All runtime images (--target gateway|operator)
├── charts/kavrynt/        # Umbrella Helm chart, vendored subcharts, schemas, unit tests
├── cmd/                   # Entry points: kavryctl, gateway, operator
├── config/                # CRD, RBAC, and operator manifests
├── docs/                  # ADRs, release and e2e runbooks, component docs
├── examples/              # kavryctl sample manifests and the demo kit
├── internal/              # Component packages
├── scripts/               # e2e, demo, installers, release and spec checks
├── specs/                 # Feature specifications (spec-driven development)
├── test/e2e/              # End-to-end fixtures
├── .github/               # CI workflows, issue and pull request templates
└── Makefile               # Every developer and CI task (make help)
```

### Release process

Releases are cut by maintainers from `main` following
[docs/RELEASE.md](docs/RELEASE.md):

- `develop` is promoted to `main`, and a tag `vX.Y.Z-(alpha|beta).N` that
  equals the chart version triggers `release.yml`.
- The workflow publishes signed multi-architecture images to GHCR and Docker
  Hub, `kavryctl` binaries for six platforms, and the OCI Helm chart, then
  validates the published release end to end.
- Published tags are never overwritten; fixes ship in the next prerelease.

Contributors do not bump versions or tag releases.

### Links and community resources

- [README](README.md): what Kavrynt does today and how to install it
- [ROADMAP.md](ROADMAP.md): what comes next
- [GOVERNANCE.md](GOVERNANCE.md) and [MAINTAINERS.md](MAINTAINERS.md): roles
  and decisions
- [SECURITY.md](SECURITY.md): private vulnerability reporting
- [Model Context Protocol](https://modelcontextprotocol.io/)
- [Developer Certificate of Origin](https://developercertificate.org/)

### Code of conduct

Everyone taking part in this project follows the
[Code of Conduct](CODE_OF_CONDUCT.md).

---

Thanks again for contributing to Kavrynt!
