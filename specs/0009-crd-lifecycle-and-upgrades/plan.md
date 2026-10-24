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
- **Upgrade test (R05, R08, R09).** A final section of
  `scripts/e2e-kind.sh` on the same disposable cluster (a second release
  would collide on cluster-scoped names): `helm uninstall` (CRD and
  `MCPServer` kept), delete the CRD, `helm install` the previous version
  from `oci://ghcr.io/kavrynt/charts/kavrynt` (`UPGRADE_FROM_VERSION`,
  pinned in the script and bumped after each release, see
  `docs/RELEASE.md`), apply an `MCPServer`, try `helm upgrade` without
  adoption (expect failure and unchanged Deployment generations, R07),
  adopt, upgrade with the locally built images, compare the installed
  CRD's versions with `config/crd/bases`, check `Ready` and `tools/list`,
  then `helm rollback 1` and check again.
- **Demo and docs.** `scripts/demo-kind.sh` drops its manual CRD apply.

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/charts/k8s-operator/crds/` → `files/` | CRD moved |
| `charts/kavrynt/charts/k8s-operator/templates/crd.yaml` | New |
| `charts/kavrynt/charts/k8s-operator/values.yaml`, schema | `crds.install`, `crds.keep` |
| `charts/kavrynt/tests/crd_test.yaml`, `values_schema_test.yaml` | CRD rendering cases |
| `scripts/verify-release-contract.sh` | New CRD path |
| `scripts/e2e-kind.sh`, `docs/E2E.md`, `docs/RELEASE.md` | Upgrade and rollback test |
| `scripts/demo-kind.sh`, `CLAUDE.md`, chart README, `README.md` | CRD steps |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0009-R01, R02, R03 | `crd_test.yaml` |
| SPEC-0009-R04 | `crd_test.yaml`; `e2e-kind.sh` installed CRD equals `config/crd/bases`; release contract `cmp` |
| SPEC-0009-R05 | `e2e-kind.sh` uninstall and rollback checks |
| SPEC-0009-R06, R07 | `e2e-kind.sh` adoption section |
| SPEC-0009-R08, R09 | `e2e-kind.sh` upgrade and rollback |

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
