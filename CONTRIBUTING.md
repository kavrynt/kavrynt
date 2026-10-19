# Contributing to Kavrynt

Thank you for your interest in Kavrynt. This guide explains how to propose a
change and what a change needs before it can be merged.

## Licensing of contributions

Kavrynt is licensed under the [Apache License 2.0](LICENSE). By contributing,
you agree that your contribution is licensed under the same license, as
described in section 5 of the Apache License.

## Developer Certificate of Origin

Every commit must be signed off under the
[Developer Certificate of Origin 1.1](https://developercertificate.org/):

```bash
git commit -s -m "Describe the change"
```

The sign-off (`Signed-off-by: Your Name <you@example.com>`) certifies that you
wrote the change or have the right to submit it. Use your real name.

## Before you start

- Search [issues](https://github.com/kavrynt/kavrynt/issues) first.
- For anything larger than a bug fix, open an issue describing the problem and
  the proposed approach before writing code.
- Changes to the architecture, the `MCPServer` API, or the security model need
  an Architecture Decision Record in `docs/` (see existing `ADR-*.md`).
- Report security vulnerabilities privately; see [SECURITY.md](SECURITY.md).

## Development setup

Toolchain: Go 1.26+, Docker, Helm 3 or 4, kubectl, and Kind.

```bash
make help       # list targets
make build      # bin/kavryctl, bin/gateway, bin/operator
make test       # go test -race ./...
make qa         # every quality and security gate CI runs
make e2e-kind   # disposable Kind cluster, full product loop
```

`make e2e-kind` creates its own Kind cluster and never touches your current
Kubernetes context. Details: [docs/E2E.md](docs/E2E.md).

## Branches and pull requests

The project uses GitFlow:

- `develop` is the default branch. Open pull requests against it.
- Name branches `feature/<short-kebab-case-name>` or
  `fix/<short-kebab-case-name>`.
- Keep one logical change per pull request.
- `main` holds released versions only; release tags are cut from `main`.

A pull request can be merged when:

1. `make qa` and the Kind end-to-end job pass in CI;
2. new behavior has tests that run without network access (use fake
   `http.RoundTripper`s and `t.TempDir()`);
3. docs and the README match what the code actually does;
4. every commit is signed off;
5. a maintainer has approved it.

## Engineering rules

- Keep components separated: the Gateway contains no reconciliation logic, the
  Operator never proxies traffic, and `kavryctl` imports only shared API types.
- Shared Kubernetes API types live only in `api/v1alpha1`. The CRD schema is
  hand-written and exists in `config/crd/bases` and in the Operator subchart;
  change both together with the Go types.
- Treat Kubernetes manifests, MCP payloads, tool metadata, and Gateway traffic
  as untrusted input.
- Prefer the standard library. New dependencies must pass govulncheck and
  Trivy.
- Never commit secrets, tokens, kubeconfigs, or private keys, and never log
  secret values.
- Do not describe unimplemented features (authentication, authorization,
  audit, high availability) as available.

## Code of conduct

Everyone taking part in this project follows the
[Code of Conduct](CODE_OF_CONDUCT.md).
