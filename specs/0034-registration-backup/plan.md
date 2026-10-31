# SPEC-0034 plan: Registration export and import

## Decisions

- Accepted on 2026-10-31 with both open questions as recommended: create
  and update instead of server-side apply; servers that are not `Ready`
  are exported.
- Namespace existence is read with `get namespaces`; a user who may not
  read namespaces is checked by the server-side dry-run create, which the
  API server refuses for a missing namespace.
- The Operator version comes from the Operator Deployment's image tag in
  `--release-namespace` (label `operator`, or `k8s-operator` for the
  subchart alone), as `kavryctl doctor` reads it.

## Design

`internal/kavryctl/cli/backup.go`, with `manifest.Marshal` (the fields
`Parse` reads, nothing else) and `manifest.SplitDocuments`.

- **Export:** list (one namespace or all), sort by namespace and name, keep
  labels and annotations without kubectl's last-applied annotation, blank
  `spec.environment` values and list their keys in
  `kavrynt.io/redacted-environment` unless `--include-environment`, check
  each document with `manifest.Parse`, and write the header (time, context
  name, Operator version, credential keys, redactions). `-o` writes mode
  `0600`.
- **Import, check:** parse every document, refuse duplicates, redaction
  annotations, missing namespaces, documents the API server refuses
  (server-side dry-run create, or dry-run update for an overwrite), and
  existing servers that differ without `--overwrite`; a file over 16 MiB is
  refused before parsing. Any refusal prints every problem and writes nothing.
- **Import, write:** create, or update with the resourceVersion read in the
  check (a concurrent change is a conflict, reported as changed by
  someone else). `--dry-run` does the writes with `DryRunAll`. Then wait
  up to `--wait` and report servers not `Ready` (reason only) and
  `CredentialUnavailable` keys.

## Affected files

| Path | Change |
| --- | --- |
| `internal/kavryctl/cli/backup.go` | `export`, `import` |
| `internal/kavryctl/manifest/manifest.go` | `Marshal`, `SplitDocuments` |
| `internal/kavryctl/cli/cli.go` | Dispatch, usage, `core/v1` in the scheme |
| `internal/kavryctl/cli/backup_test.go` | Tests |
| Docs | kavryctl docs (Rebuilding a cluster), `ROADMAP.md`, `CLAUDE.md` |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `cli.TestExport` |
| R02 | `cli.TestExport` (the client fails the test on any Secret access) |
| R03 | `cli.TestExport` |
| R04 | `cli.TestImportRefusesBeforeWriting`, `cli.TestImportRoundTrip`, `cli.TestImportConflict` |
| R05 | `cli.TestImportRoundTrip` |
| R06 | `cli.TestImportDryRun` |
| R07 | `cli.TestImportRefusesBeforeWriting`, `cli.TestImportRoundTrip` |
| R08 | `cli.TestImportReportsReadiness`, `cli.TestImportWaitsForTheNewGeneration` |
| R09 | `cli.TestExportRedactsArgsAndKeepsFilesPrivate`, `cli.TestImportChecksBeforeOverwriting` |
| R10 | `cli.TestImportChecksBeforeOverwriting` |

Also run against a real Kind cluster with three MCP servers: export, an
import dry run reporting every server unchanged, and the restore of a
deleted server to `Ready` with its tools discovered.

## Rollout

New commands in the next release.

## Risks

An export holds endpoints and labels; the docs say to store it like other
cluster configuration, and as a secret with `--include-environment`.
