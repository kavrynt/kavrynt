# SPEC-0009 plan: CRD lifecycle and upgrades

## Design

- **Move.** `git mv charts/kavrynt/charts/k8s-operator/crds/kavrynt.io_mcpservers.yaml`
  to `charts/kavrynt/charts/k8s-operator/files/kavrynt.io_mcpservers.yaml`
  (unchanged bytes, so R04 stays a `cmp`). New
  `templates/crd.yaml` renders it with `.Files.Get` inside
  `{{- if .Values.crds.install }}`, injecting the keep annotation and chart
  labels into `metadata` (R01–R03). Injection is done by parsing with
  `fromYaml`, merging annotations and labels, and `toYaml`; the spec is
  not modified, so its content is the file's.
- **Release contract (R04).** `scripts/verify-release-contract.sh` compares
  `config/crd/bases` with `files/`. A chart unit test asserts the rendered
  `spec` equals the file's `spec`.
- **Adoption (R06, R07).** Chart README:

  ```bash
  kubectl label crd mcpservers.kavrynt.io app.kubernetes.io/managed-by=Helm --overwrite
  kubectl annotate crd mcpservers.kavrynt.io \
    meta.helm.sh/release-name=kavrynt meta.helm.sh/release-namespace=kavrynt-system --overwrite
  ```

  Helm validates ownership of every resource before applying any, so a
  missing adoption fails the whole upgrade (R07); the e2e checks the
  Deployments' generation is unchanged after the failed attempt.
- **Upgrade test (R08, R09).** New `scripts/e2e-upgrade.sh`, called from
  `make e2e-kind` after the main loop, in its own namespace and release on
  the same disposable cluster: `helm install` the previous version from
  `oci://ghcr.io/kavrynt/charts/kavrynt` (`UPGRADE_FROM_VERSION`, default:
  the newest `v*` tag reachable from `HEAD` that is not the chart version),
  apply an `MCPServer`, try `helm upgrade` without adoption (expect
  failure, R07), adopt, upgrade with the locally built images, check
  `Ready`, `tools/list`, and `status.serverInfo` in the schema, then
  `helm rollback 1` and check again.
- **Demo and docs.** `scripts/demo-kind.sh` drops its manual CRD apply.

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/charts/k8s-operator/crds/` → `files/` | CRD moved |
| `charts/kavrynt/charts/k8s-operator/templates/crd.yaml` | New |
| `charts/kavrynt/charts/k8s-operator/values.yaml`, schema | `crds.install`, `crds.keep` |
| `charts/kavrynt/tests/operator_test.yaml` | CRD rendering cases |
| `scripts/verify-release-contract.sh` | New CRD path |
| `scripts/e2e-upgrade.sh`, `Makefile`, `docs/E2E.md` | Upgrade and rollback test |
| `scripts/demo-kind.sh`, `CLAUDE.md`, chart README, `README.md` | CRD steps |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0009-R01, R02, R03 | `operator_test.yaml` |
| SPEC-0009-R04 | `operator_test.yaml` spec equality; release contract |
| SPEC-0009-R05 | `e2e-upgrade.sh` rollback section; uninstall check |
| SPEC-0009-R06, R07 | `e2e-upgrade.sh` adoption section |
| SPEC-0009-R08, R09 | `e2e-upgrade.sh` |

## Rollout

Default on. Release notes lead with the adoption command for existing
installs. GitOps users who manage CRDs themselves set
`operator.crds.install=false`.

## Risks

- Large CRDs can exceed Helm's release Secret size; the current CRD is
  small (well under 1 MiB). Detected by the e2e install.
- Installers without permission to update CRDs (namespace-scoped Helm
  users) now fail on upgrade. They set `operator.crds.install=false` and
  have a cluster administrator apply the CRD; documented.
