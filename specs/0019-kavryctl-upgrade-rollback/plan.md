# SPEC-0019 plan: kavryctl upgrade and rollback

## Design

- **Commands.** `internal/kavryctl/cli/upgrade.go`: `runUpgrade` and
  `runRollback`, wired in `Execute` and the usage text. Flags: the cluster
  flags, `--release` (default `kavrynt`), `--namespace` (default
  `kavrynt-system`), `--chart` (default
  `oci://ghcr.io/kavrynt/charts/kavrynt`), `--version`, `--values FILE`,
  `--adopt-crd`, `--timeout` (default `5m`), `--dry-run`; rollback takes
  `--revision N`.
- **Helm runner (R01).** A `helmRunner` interface (`Run(ctx, args...)
  (stdout, stderr []byte, err)`) with an `execHelm` implementation found
  with `exec.LookPath("helm")`; a missing binary gives "kavryctl upgrade
  needs the helm binary on PATH: https://helm.sh/docs/intro/install/".
  Tests replace the runner, as they replace the Kubernetes client.
  `helm` gets `--kube-context` and `--kubeconfig` from the cluster flags.
- **Preflight (R02).** In order, changing nothing:
  1. `helm status <release> -n <ns> -o json`: the release exists.
  2. `--version` required unless `--chart` is a directory;
     `helm show chart <chart> --version <v>` succeeds.
  3. The CRD `mcpservers.kavrynt.io`, read as metadata only
     (`metav1.PartialObjectMetadata`), carries
     `app.kubernetes.io/managed-by: Helm` and
     `meta.helm.sh/release-name`/`-namespace` of this release. If not, stop
     and print the two `kubectl` commands from the chart README, unless
     `--adopt-crd`, which applies the same label and annotations by patch.
- **Upgrade (R03).** Before: the `Ready=True` servers in every namespace.
  Then `helm upgrade <release> <chart> --version <v> --reuse-values
  [--values FILE] --wait --timeout <t>`. After: poll up to `--timeout` until
  every server routed before is `Ready=True` again; print one line per
  server and exit `1` naming those that are not. Values are never printed.
- **Rollback (R04).** Step 1 of the preflight (the CRD is kept on rollback
  and needs no check), the same before and after check around
  `helm rollback <release> [N] --wait --timeout <t>`.
- **Dry run (R05).** Runs the read-only checks, prints them and the Helm
  command (with `--adopt-crd`, the patch it would make), and runs nothing
  that changes the cluster.
- **Boundaries (R06).** A test parses every Go file of `cmd/kavryctl` and
  `internal/kavryctl` and fails on imports of `internal/gateway`,
  `internal/operator`, or other internal packages outside
  `internal/kavryctl`.
- **E2E.** In the SPEC-0009 section, the rollback runs as
  `kavryctl rollback --revision 1`, then `kavryctl upgrade --chart
  <local chart> --values <local images>` moves forward again and reports
  the server routed.

## Affected files

| Path | Change |
| --- | --- |
| `internal/kavryctl/cli/upgrade.go`, `upgrade_test.go`, `boundaries_test.go` | Commands and tests |
| `internal/kavryctl/cli/cli.go` | Wiring, usage |
| `scripts/e2e-kind.sh` | Rollback and re-upgrade with `kavryctl` |
| `docs/components/kavryctl/README.md`, chart README, README | Docs |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0019-R01 | `upgrade_test.go`: no `helm` on PATH |
| SPEC-0019-R02 | `upgrade_test.go`: missing release, missing version, CRD not owned (stops, no Helm upgrade call), `--adopt-crd` (patched) |
| SPEC-0019-R03 | `upgrade_test.go`: arguments include `--reuse-values`; a server that stays not Ready exits `1`; `e2e-kind.sh` |
| SPEC-0019-R04 | `upgrade_test.go`: rollback arguments and checks; `e2e-kind.sh` |
| SPEC-0019-R05 | `upgrade_test.go`: dry run makes no changing call |
| SPEC-0019-R06 | `boundaries_test.go` |

## Rollout

New commands; release notes and the chart README's upgrade section.

## Risks

- `helm` output formats change between versions. Mitigation: only
  `helm status -o json` is parsed, and only for existence.
