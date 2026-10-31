# SPEC-0033 plan: kavryctl doctor

## Decisions

- Accepted on 2026-10-31 with both open questions as recommended: the
  first version checks what breaks a trial; optional features come later;
  versions come from image tags.
- The release is found as `upgrade` finds it: `-n` (default
  `kavrynt-system`) and `--release` (default `kavrynt`), Deployments
  selected by `app.kubernetes.io/name` (`gateway`; `operator` in the
  umbrella chart, `k8s-operator` from the subchart alone) and
  `app.kubernetes.io/instance`. Found against a real cluster: the umbrella
  chart's Operator label is `operator`.
- A cluster that does not answer the first request (the CRD `get`) with an
  API status is unreachable: exit `2`.

## Design

`internal/kavryctl/cli/doctor.go`. Checks in order, each `ok`, `warn`,
`fail`, or `skipped` with a stable ID: `crd`, `gateway`, `operator`,
`versions`, `auth` (the Gateway container has `--auth`), then
`server.ready` and `server.discovery` per `MCPServer`. A forbidden request
makes its check `skipped`, naming the verb and resource. Fixes come from
fixed text per condition reason; condition messages are never read into
the report. `--anonymize` uses an in-memory HMAC-SHA256 key per run, 12 hex
characters, lengthened by 4 on a collision. The kavryctl client scheme
gains `apps/v1`.

## Affected files

| Path | Change |
| --- | --- |
| `internal/kavryctl/cli/doctor.go` | The command |
| `internal/kavryctl/cli/cli.go` | Dispatch, usage, `apps/v1` in the scheme |
| `internal/kavryctl/cli/doctor_test.go` | Tests |
| Docs | kavryctl docs, `docs/TESTING.md`, the tester feedback template, `ROADMAP.md`, `CLAUDE.md` |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `cli.TestDoctorHealthyInstallation` (the client fails the test on any write or Secret access, in every doctor test) |
| R02 | `cli.TestDoctorSkipsWhatItMayNotRead` |
| R03 | `cli.TestDoctorHealthyInstallation`, `cli.TestDoctorFailuresAndWarnings`, `cli.TestDoctorSkipsWhatItMayNotRead` |
| R04 | `cli.TestDoctorHealthyInstallation`, `cli.TestDoctorFailuresAndWarnings`, `cli.TestDoctorVersions` |
| R05 | `cli.TestDoctorFailuresAndWarnings` |
| R06 | `cli.TestDoctorHealthyInstallation` |
| R07 | `cli.TestDoctorFailuresAndWarnings` |
| R08 | `cli.TestDoctorAnonymize` |

## Rollout

A new command in the next release. The tester feedback template asks for
its `--anonymize` output.

## Risks

A release installed with a non-standard name or namespace needs `--release`
and `-n`; the `NotFound` fix says so.
