# SPEC-0050: kavryctl flag parsing migration to pflag

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @vak-rashu |
| Created | 2026-11-26 |
| Issue | #212 |
| ADRs | none |
| Supersedes | none |

## Summary

`kavryctl` migrates its command-line flag parsing from Go's standard library
`flag` package to `github.com/spf13/pflag`. This enables interspersed flag
parsing (accepting flags before, between, or after positional arguments) across
all commands, introduces native POSIX flag shorthands, standardizes flag error
reporting to standard error across all subcommands, and aligns `kavryctl`'s CLI
behavior with Kubernetes and `kubectl` conventions.

## Problem

Users accustomed to `kubectl` and standard POSIX command-line tools frequently
pass flags after positional arguments (for example,
`kavryctl inspect sample -n kavrynt-try`). Under Go's standard library `flag`
package, flag parsing terminates unconditionally at the first non-flag
positional argument. As a result, flags placed after positional arguments are
treated as trailing operands, causing commands to fail with argument-count usage
errors ([#172](https://github.com/kavrynt/kavrynt/issues/172)).

However, migrating to `github.com/spf13/pflag` cannot be done as a one-line
drop-in replacement due to hardcoded assumptions in `kavryctl` (#212):

1. **Broken Shorthand Flags:** Standard `flag` lacks native shorthand support,
   so `kavryctl` previously registered shorthands by duplicating flags
   (e.g. `fs.StringVar(&ns, "n", ...)` and `fs.BoolVar(all, "A", ...)`). In
   `pflag`, registering `"n"` or `"A"` creates `--n` and `--A` rather than
   single-hyphen shorthands (`-n`, `-A`), breaking existing short flags.
2. **Inconsistent / Silent Error Reporting:** `pflag.FlagSet` in
   `ContinueOnError` mode does not automatically print parse errors to standard
   error. Several `kavryctl` subcommands relied on `flag`'s automatic error
   printing and exit with code `2` upon parse errors without explicitly writing
   the error message to stderr, silently swallowing errors.
3. **Help Text and Error Formatting:** `pflag` formats flag defaults and
   unknown flag errors differently (e.g. `unknown flag: --foo` vs.
   `flag provided but not defined: -foo`), which requires updating CLI help
   and error-handling assertions across unit tests.
4. **Ad-hoc Parsing in Multi-level Subcommands:** Because standard `flag`
   could not handle flags after positional operands, `audit verify` bypassed
   `FlagSet` entirely and relied on ad-hoc argument slice scanning
   (`for _, a := range args[1:]`) to detect `-h` and `--help` before or after
   the optional file operand. Migrating to `pflag` allows `audit verify` to be
   standardized on `newFlagSet` like all other subcommands.

Resolving #172 safely and robustly requires a deliberate migration of
`kavryctl`'s flag parsing foundation to `pflag`.

## Goals

- Support interspersed flags across all `kavryctl` subcommands so that flags
  may appear before, after, or between positional arguments (resolving #172).
- Migrate all `kavryctl` subcommands to `github.com/spf13/pflag` without
  introducing new external dependencies (`pflag` is already an indirect
  dependency via `client-go`).
- Standardize all subcommands, including multi-level subcommands like
  `audit verify`, to use `pflag`-backed `FlagSet` instances instead of manual
  argument slice loops.
- Register short flags (`-n`, `-A`, `-o`) as first-class POSIX shorthands
  using `pflag`'s `...P` binding methods.
- Ensure all subcommands consistently print parse errors and command usage to
  standard error and exit with code `2`.
- Preserve existing help text ergonomics (help written to standard output with
  exit `0`, operational caveats preserved).

## Non-goals

- Adopting a full CLI application framework such as Cobra; `kavryctl` retains
  its lightweight command dispatch architecture in `cli.Execute`.
- Changing `kavryctl inspect` output formatting or default view (part 2 of
  #172, which is tracked separately and requires its own spec).
- Changing existing command names, flag names, flag defaults, or exit codes
  (`0` for success/help, `1` for runtime/cluster failures, `2` for usage/flag
  errors).

## Requirements

- **SPEC-0050-R01** Every `kavryctl` subcommand that accepts flags and positional
  arguments (`register`, `unregister`, `inspect`, `validate`, `export`,
  `import`, `upgrade`, `rollback`, `doctor`, and `audit verify`) MUST support
  interspersed flags, parsing flags correctly regardless of whether they appear
  before, after, or between positional arguments.
- **SPEC-0050-R02** All single-letter shorthand flags (`-n` for `--namespace`,
  `-A` for `--all-namespaces`, and `-o` for `--output` / `-o <file>`) MUST be
  bound as native shorthands using `pflag`'s shorthand methods (`StringVarP`,
  `BoolVarP`, `StringP`) rather than as duplicate long flags.
- **SPEC-0050-R03** When flag parsing fails on any `kavryctl` subcommand (due to
  an unknown flag, invalid flag value, or missing flag argument), `kavryctl`
  MUST write the error message and the command's usage to standard error,
  MUST NOT write to standard output, and MUST exit with code `2`.
- **SPEC-0050-R04** Help output requested via `-h`, `--help`, or
  `kavryctl help <command>` MUST write to standard output, MUST exit with code
  `0`, and MUST present long flags and their shorthands in POSIX format
  (e.g. `-n, --namespace string`).
- **SPEC-0050-R05** `kavryctl`'s CLI package (`internal/kavryctl/cli`) MUST use
  `github.com/spf13/pflag` for all command `FlagSet` instances, MUST standardize
  nested subcommands (`audit verify`) to use a `pflag`-backed `FlagSet` instead of
  manual argument loop scanning, and MUST NOT import Go's standard library `flag` package.

## Acceptance criteria

- Given an existing `MCPServer` `payments` in namespace `team-a`, when a user runs
  `kavryctl inspect payments -n team-a` or `kavryctl inspect -n team-a payments`,
  then both invocations resolve namespace `team-a` and server `payments`, write
  the inspected output to standard output, and exit `0` (SPEC-0050-R01, SPEC-0050-R02).
- Given a user runs `kavryctl list -A` or `kavryctl list --all-namespaces`,
  then all namespaces are queried without an unknown shorthand error and the
  command exits `0` (SPEC-0050-R02).
- Given a user passes an unknown flag (such as `kavryctl list --bogus` or
  `kavryctl inspect payments -x`), then an unknown flag error and usage are
  written to standard error, standard output is empty, and the command exits `2`
  (SPEC-0050-R03).
- Given a user runs `kavryctl <command> --help` or `kavryctl help <command>`,
  then the command synopsis and flags (including `-h`, `--help` and any shorthands)
  are written to standard output, standard error is empty, and the command exits
  `0` (SPEC-0050-R04).
- Given a source code inspection of `internal/kavryctl/cli/`, no Go file imports
  the standard library `flag` package (SPEC-0050-R05).

## Security and privacy

This change affects only command-line argument tokenization and flag parsing.
It introduces no new network access, cluster permissions, or credential
exposure. Sensitive values (such as tokens or environment values in manifests)
continue to be handled according to existing registration and export security
boundaries (SPEC-0034).

## Compatibility

- **CLI Compatibility:** Fully backwards-compatible for all existing valid flag
  invocations. Flags placed after positional arguments, previously rejected with
  exit `2`, are now accepted.
- **Error Messages:** Error messages for unrecognized flags transition from Go
  `flag` format (`flag provided but not defined: -foo`) to `pflag` format
  (`unknown flag: --foo`).
- **Dependencies & CRDs:** `github.com/spf13/pflag` is already present in
  `go.mod` as a dependency of Kubernetes client packages. No new direct
  dependencies are added. No CRD or Helm value changes are required.

## Documentation

The `kavryctl` README (`docs/components/kavryctl/README.md`) will be updated to
reflect that flags may be placed anywhere in command invocations and to document
supported flag shorthands.

## Open questions

- None.
