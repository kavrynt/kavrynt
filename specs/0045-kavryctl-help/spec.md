# SPEC-0045: kavryctl per-command help and exit codes

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @vak-rashu |
| Created | 2026-11-10 |
| Issue | #154 |
| ADRs | none |
| Supersedes | none |

## Summary

`kavryctl` supports `help`, `-h`, and `--help` at both the top level and on
individual subcommands, writing help text to standard output and exiting `0`,
while keeping parse errors and error usage on standard error with exit `2`.
Every command has a concise one-line summary at the top level and focused
per-command help that preserves existing operational caveats.

## Problem

Running `kavryctl` with no arguments currently prints usage to standard error
and exits `2`, and running `kavryctl <command> -h` or `--help` writes flag
help to standard error and exits `2` as well. Furthermore, top-level usage mixes
every command's flags and notes into one long screen, while command-specific
help like `kavryctl export --help` or `kavryctl audit verify --help` is hard
to inspect cleanly in a pager or pipeline, and parse errors on `flag.FlagSet`
must never contaminate redirected standard output.

## Goals

- Consistent CLI help ergonomics: help goes to standard output with exit `0`,
  and invalid invocations go to standard error with exit `2`.
- Short, scannable top-level help with a one-line description per command and
  clearly separated help vs. cluster flags.
- Per-command `-h` / `--help` for subcommands without hitting the cluster or
  opening input files.

## Non-goals

- Adopting an external CLI framework (such as Cobra); `kavryctl` continues to
  use the Go standard library `flag` package.
- Changing the runtime behavior, cluster permissions, or exit codes (`0`, `1`,
  `2`) of successful or failed command execution beyond help and parse-error
  handling.

## Requirements

- **SPEC-0045-R01** Running `kavryctl` with no arguments, `help`, `-h`, or
  `--help` MUST print top-level usage (including a one-line description per
  command, a `Flags` section with `-h`/`--help`, and `Cluster flags`) to
  standard output and exit `0`.
- **SPEC-0045-R02** Each flag-based subcommand (`version`, `validate`, `register`,
  `unregister`, `list`, `inspect`, `export`, `import`, `doctor`, `upgrade`,
  `rollback`, and `audit verify`) MUST accept `-h` and `--help`, print its
  command description, `[flags]` synopsis, and bound flags to standard output,
  and exit `0` without loading manifests, opening input files, calling Helm,
  or contacting Kubernetes. Commands that take a file or a named argument also 
  show `<manifest.yaml|json>` or `<name>` in the synopsis.
- **SPEC-0045-R03** `kavryctl audit -h` and `kavryctl audit --help` MUST print
  the `audit` summary, synopsis, and subcommands (`verify`) to standard output
  and exit `0`. `kavryctl audit` with no arguments or an unknown subcommand
  MUST print usage to standard error and exit `2`.
- **SPEC-0045-R04** On an unknown top-level command, unknown flag, invalid flag
  value, or wrong positional argument count, `kavryctl` MUST write the error
  message and usage text to standard error, write nothing to standard output,
  and exit `2`.
- **SPEC-0045-R05** Per-command help MUST preserve the critical operational
  notes previously shown in top-level usage and reflect optional flags
  accurately:
  1. `audit verify --help` MUST state that events cut off the end of a chain
     are not detected.
  2. `export --help` and `import --help` MUST state that environment values
     and arguments are written empty by default, Secrets are never read, and
     `import` changes nothing if preflight refuses any document, but later
     write failures can leave partial changes.
  3. `doctor --help` MUST state that `doctor` is read-only and that `-n`
     defaults to `kavrynt-system`.
  4. `upgrade --help` MUST present flags via `[flags]` rather than marking
     `--version` mandatory, since `--chart <dir>` accepts a local chart
     without `--version`.
- **SPEC-0045-R06** `kavryctl help <command>` (including
  `kavryctl help audit` and `kavryctl help audit verify`) MUST print the same
  help to standard output and exit `0` as `kavryctl <command> --help`; when
  `<command>` is unknown, it MUST write an error and top-level usage to
  standard error and exit `2`.

## Acceptance criteria

- Given any user runs `kavryctl`, `kavryctl help`, `kavryctl -h`, or
  `kavryctl --help`, then top-level usage is written to standard output,
  standard error is empty, and the exit code is `0` (SPEC-0045-R01).
- Given any user runs `kavryctl <cmd> --help` or `kavryctl help <cmd>` on a
  subcommand (such as `list`, `export`, or `upgrade`), then that command's
  help is written to standard output, standard error is empty, and the exit
  code is `0` without reading a kubeconfig or contacting a cluster
  (SPEC-0045-R02, R05, R06).
- Given any user runs `kavryctl audit -h`, `kavryctl audit verify --help`, or
  `kavryctl help audit verify` (even if a nonexistent file argument follows),
  then help is written to standard output, no file is opened, and the exit
  code is `0` (SPEC-0045-R02, R03, R05, R06).
- Given any user runs `kavryctl export --bad > out.yaml`, then standard output
  (`out.yaml`) remains empty, the error and usage are written to standard
  error, and the exit code is `2` (SPEC-0045-R04).

## Security and privacy

Help and parse-error usage messages contain only static command documentation
and flag definitions. Invoking `-h` or `--help` exits before reading
kubeconfigs, Secrets, manifests, or audit log files, and routing parse errors
strictly to standard error prevents corrupting redirected export or inspect
output streams.

## Compatibility

- Invoking `kavryctl` with no arguments changes from exit `2` on standard
  error to exit `0` on standard output, matching standard CLI conventions.
- Subcommand `-h` / `--help` changes from exit `2` on standard error to exit
  `0` on standard output.
- No changes to the `MCPServer` CRD, API versions, Helm values, or command
  runtime behavior; no CRD or upgrade migration is needed.

## Documentation

- [docs/components/kavryctl/README.md](../../docs/components/kavryctl/README.md):
  add a short note that `kavryctl <command> --help` shows per-command flags
  and behavior (once this spec becomes `Implemented`).

## Open questions

- None.
