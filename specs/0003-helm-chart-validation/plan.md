# SPEC-0003 plan: Helm chart values validation and unit tests

## Design

- **Schemas.** Each subchart has a hand-written draft-07
  `values.schema.json` with `additionalProperties: false` on every object the
  chart defines. Helm validates subchart values against the subchart schema
  when installing the umbrella chart, so `gateway.*` and `operator.*` are
  covered there. The umbrella schema covers the top level and
  `global.watchNamespaces`. `global` stays open in all three.
- **Pass-through objects** (`resources`, `affinity`, `tolerations`,
  `nodeSelector`, security contexts) are typed as objects or arrays only.
- **Cross-field rules** stay in the templates: the Cloud connector's `https`
  URL and Secret name, and the removed `gateway.config.watchNamespaces`, whose
  template error message is more helpful than a schema error. The schema
  therefore still allows `gateway.config.watchNamespaces`.
- **Unit tests** live in `charts/kavrynt/tests/` and render the umbrella
  chart, so values are set exactly as users set them. `.helmignore` keeps the
  tests out of the packaged chart. `make helm-unittest` runs them; it is part
  of `make qa` and the CI Helm job. The plugin version is pinned in the
  Makefile (`HELM_UNITTEST_VERSION`).

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/values.schema.json` | Umbrella schema |
| `charts/kavrynt/charts/gateway/values.schema.json` | Gateway schema |
| `charts/kavrynt/charts/k8s-operator/values.schema.json` | Operator schema |
| `charts/kavrynt/tests/*_test.yaml` | helm-unittest suites |
| `charts/kavrynt/.helmignore` | Exclude tests from the package |
| `Makefile`, `.github/workflows/qa.yml` | `helm-plugins` and `helm-unittest` targets and CI steps |
| `scripts/verify-specs.sh` | Chart tests count as citations |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0003-R01 | `values_schema_test.yaml` mistyped gateway, top-level, and operator keys; platform globals |
| SPEC-0003-R02 | `values_schema_test.yaml` replica count, console switch, pull policy |
| SPEC-0003-R03 | `values_schema_test.yaml` origin with path, without scheme, with comma; port and slash accepted |
| SPEC-0003-R04 | `values_schema_test.yaml` four duration values; compound durations accepted |
| SPEC-0003-R05 | `values_schema_test.yaml` invalid and duplicate namespaces |
| SPEC-0003-R06 | `gateway_test.yaml` cluster-wide and per-namespace RBAC |
| SPEC-0003-R07 | `gateway_test.yaml` and `operator_test.yaml` container hardening; service account automount |
| SPEC-0003-R08 | `gateway_test.yaml` console on and off |
| SPEC-0003-R09 | `gateway_test.yaml` origins and stripped headers flags |
| SPEC-0003-R10 | `operator_test.yaml` https URL, Secret name, read-only token mount |
| SPEC-0003-R11 | `gateway_test.yaml` removed `gateway.config.watchNamespaces` |

## Rollout

Ships in the next prerelease. Release notes: installs with unknown or
malformed values now fail at `helm install` or `helm upgrade`; run
`helm template` with your values file before upgrading.

## Risks

- A new value added to `values.yaml` without a schema entry fails
  `make helm-lint`. That is intended: the schema is the contract.
- Platforms that inject keys outside `global` into chart values would be
  rejected. None is known; `global` stays open for the common case.
