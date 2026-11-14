# SPEC-0045 plan: kavryctl per-command help and exit codes

## Design

`kavryctl` continues to use Go's standard library `flag` package without external CLI frameworks.

- **Help routing and exit codes:**
  - `kavryctl`, `kavryctl help`, `kavryctl -h`, and `kavryctl --help` print top-level usage to standard output and exit `0`.
  - Subcommands configure a `flag.FlagSet` via `newFlagSet(name, description, synopsis, stdout, stderr)`. When `-h` or `--help` is parsed, the standard library returns `flag.ErrHelp`. The command handler intercepts `errors.Is(err, flag.ErrHelp)`, formats help to standard output, and returns exit code `0` immediately without reading kubeconfig, opening input files, or contacting the Kubernetes cluster.
  - Multi-character flags print with double dashes (`--flag`) and single-character flags with a single dash (`-flag`).
  - `kavryctl help <command>` (and nested commands such as `kavryctl help audit verify`) forwards to `Execute(append(args, "--help"))`, ensuring identical help output on standard output and exit `0`. An unknown command writes an error and usage to standard error and exits `2`.
- **Top-level usage:**
  - Concise one-line command descriptions aligned with spaces (not tabs).
  - Clearly separated `Flags` section (`-h, --help`) and `Cluster flags` (`--kubeconfig`, `--context`, `-n/--namespace`).
- **Operational notes in per-command help:**
  - `export` and `import`: state that environment values and arguments are written empty by default, Secrets are never read, and import changes nothing if preflight refuses any document, but later write failures can leave partial changes.
  - `audit verify`: states that events cut off the end of a chain are not detected.
  - `doctor`: states that doctor is read-only and that `-n` defaults to `kavrynt-system`.
  - `upgrade`: states that `--version` is required unless `--chart` is a directory, with synopsis showing `[flags]`.
- **Error handling:**
  - Unknown commands, unknown flags, bad values, and wrong argument counts write error and usage messages strictly to standard error and exit `2`, writing nothing to standard output.

## Affected files

| Path | Change |
| --- | --- |
| `internal/kavryctl/cli/cli.go` | Top-level usage formatting, `runHelp`, `newFlagSet` helper, `--` flag printing, distinct audit help |
| `internal/kavryctl/cli/backup.go` | Per-command help and operational notes for `export` and `import` |
| `internal/kavryctl/cli/doctor.go` | Per-command help and operational note for `doctor` |
| `internal/kavryctl/cli/upgrade.go` | Per-command help and operational notes for `upgrade` and `rollback` |
| `internal/kavryctl/cli/cli_test.go` | Unit tests for SPEC-0045 requirements R01 through R06 |
| `docs/components/kavryctl/README.md` | Document `kavryctl <command> --help` behavior |

## Verification

Every requirement maps to at least one test that cites its ID:

| Requirement | Test |
| --- | --- |
| SPEC-0045-R01 | `internal/kavryctl/cli/cli_test.go` `TestCLIHelp` |
| SPEC-0045-R02 | `internal/kavryctl/cli/cli_test.go` `TestCLIHelp`, `TestAuditHelp` |
| SPEC-0045-R03 | `internal/kavryctl/cli/cli_test.go` `TestAuditHelp` |
| SPEC-0045-R04 | `internal/kavryctl/cli/cli_test.go` `TestCLIErrorHandling` |
| SPEC-0045-R05 | `internal/kavryctl/cli/cli_test.go` `TestOperationalNotesInHelp` |
| SPEC-0045-R06 | `internal/kavryctl/cli/cli_test.go` `TestCLIHelp`, `TestAuditHelp`, `TestCLIErrorHandling` |

## Rollout

Shipped in the next `kavryctl` binary release. No Helm values or feature flags needed.

## Risks

Existing scripts that expect `kavryctl` with no arguments to exit `2` will observe exit `0`, and scripts expecting help output on standard error will observe it on standard output. This change brings `kavryctl` into compliance with standard POSIX and GNU CLI conventions.
