# SPEC-0034: Registration export and import

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-30 |
| Issue | to be opened |
| ADRs | [ADR-0002](../../docs/ADR-0002-Remove-In-Cluster-Registry.md) |
| Supersedes | none |

## Summary

`kavryctl export` writes the `MCPServer` registrations of a cluster as
clean, reapplyable manifests, with a list of the upstream credentials they
need but never their values. `kavryctl import` applies such a file to
another (or a rebuilt) cluster, refuses to overwrite different
registrations unless told to, and reports which credentials are missing.

## Problem

Registrations live only in the Kubernetes API (ADR-0002). Restoring them
into a new cluster, or moving them, means `kubectl get -o yaml` and removing
status, UIDs, resource versions, and managed fields by hand, then finding
out from `Ready` conditions which upstream credentials the new cluster
lacks. The roadmap lists recovery workflows as missing, and production
evaluations ask how a cluster is rebuilt.

## Goals

- A file that restores every registration and nothing else.
- A restore that never silently replaces a different registration.
- A checklist of the credentials to provide, without exposing them.

## Non-goals

- Backing up Secrets, Helm values, or policies. Helm values come from
  `helm get values`; policies and Secrets belong to the platform team's own
  secret management and GitOps.
- Scheduled backups or storage targets; Velero and GitOps already do that.
- Migrating between `MCPServer` API versions.

## Requirements

### Export

- **SPEC-0034-R01** `kavryctl export [-n NAMESPACE | --all-namespaces]
  [-o FILE]` MUST write every `MCPServer` in scope as YAML documents
  holding `apiVersion`, `kind`, `metadata.name`, `metadata.namespace`,
  `metadata.labels`, `metadata.annotations` (without
  `kubectl.kubernetes.io/last-applied-configuration`), and `spec`; nothing
  else, in a stable order (namespace, then name). Every value of
  `spec.environment` MUST be written empty, since the map is free text that
  can hold a key, and the document MUST carry the annotation
  `kavrynt.io/redacted-environment` listing those keys, unless
  `--include-environment` is given, which MUST print a warning that the
  file then may hold credentials. `-o` writes the file with mode `0600`,
  also when it existed with a wider mode.
- **SPEC-0034-R02** The file MUST begin with a comment naming the export
  time, the source kubeconfig context name (never the API server address),
  the Operator version (the
  image tag of the Operator Deployment in `--release-namespace`, default
  `kavrynt-system`, or `unknown` when it cannot be read), and, for each
  server with
  `spec.credential`, the upstream credentials Secret keys it needs
  (`<namespace>.<name>`, `<namespace>.<name>.origin`). It MUST NOT contain
  Secret data and MUST NOT read Secrets.
- **SPEC-0034-R03** Every document MUST pass `kavryctl validate`.

- **SPEC-0034-R09** Every value of `spec.args` MUST be written empty, and
  the document MUST carry the annotation `kavrynt.io/redacted-args` with
  their number, unless `--include-args` is given, which MUST print the same
  warning: an argument such as `--token=...` can hold a key. `import` MUST
  refuse a document that still carries the annotation (R04).

### Import

- **SPEC-0034-R04** `kavryctl import FILE` MUST check every document before
  writing any, and change nothing when any is invalid, when two documents
  name the same namespace and name, when a document still carries the
  `kavrynt.io/redacted-environment` annotation (fill in the values, then
  remove it), or when any document would be refused by R05 or R07. The
  message MUST name every such document and, for redacted values, the keys
  to fill in. A server changed by someone else between the check and the
  write is refused then, and the report names it.
- **SPEC-0034-R05** For each document, `import` MUST create the
  `MCPServer` when it does not exist, leave it alone when its `spec`,
  labels, and annotations (as exported) are equal, and refuse it when any
  of them differs, unless `--overwrite` is given, which replaces them.
  Refused documents MUST be listed, and the exit code MUST be `1`.
- **SPEC-0034-R06** `--dry-run` MUST report what `import` would create,
  leave, refuse, or overwrite, using server-side dry run, and change
  nothing.
- **SPEC-0034-R07** `import` MUST NOT create namespaces. A document whose
  namespace does not exist MUST be refused with a message naming it.
- **SPEC-0034-R08** After applying, `import` MUST wait up to `--wait`
  (default `60s`, `0` to skip) for each imported server's conditions of
  its current generation (an overwrite's old `Ready` does not count) and
  report the servers that are not `Ready`, with their reason, and those
  whose `ToolsDiscovered` is `False` with reason `CredentialUnavailable`,
  naming the upstream credentials Secret keys to provide.
- **SPEC-0034-R10** `import` MUST refuse a file larger than 16 MiB before
  parsing any of it.

## Acceptance criteria

- Given two namespaces with three `MCPServer`s, one with `spec.credential`,
  when a platform engineer runs `kavryctl export --all-namespaces -o
  backup.yaml`, then the file has three documents without `status`,
  `uid`, `resourceVersion`, or `managedFields`, and its header names the
  two credential keys of the one server (R01, R02).
- Given that file and an empty cluster with Kavrynt and both namespaces,
  when they run `kavryctl import backup.yaml`, then three servers are
  created and the report says the credential server's tools could not be
  discovered (`CredentialUnavailable`) and names its keys (R05, R08).
- Given an existing server whose `spec.endpoint` differs, then `import`
  refuses the whole file before writing anything and exits `1`; with
  `--overwrite` it replaces that server (R04, R05).
- Given `--dry-run`, then nothing changes (R06).
- Given a file with one invalid document, two documents for the same
  server, a document with the `kavrynt.io/redacted-environment`
  annotation, or one whose server exists with another `spec`, then nothing
  is applied (R04).
- Given a server with `spec.environment`, when it is exported without
  `--include-environment`, then its values are empty and the annotation
  lists their keys; a value that is literally `<redacted>` survives an
  export with `--include-environment` and imports (R01, R04).

## Security and privacy

The export holds endpoints and labels, which can name internal services;
the documentation says to store it like other cluster configuration. It
never holds Secret data, and neither command reads Secrets.
`spec.environment` values are redacted by default because the API lets
them hold plain-text keys. `import` applies untrusted files only after the
same validation as `kavryctl register`, and CRD validation still applies at
admission. Endpoint scope (SPEC-0029) is checked by the Operator, not at
admission: an imported server whose endpoint is out of scope is created
and stays not `Ready` with `EndpointNotAllowed`, which R08 reports.

## Compatibility

New `kavryctl` commands; no CRD or chart change. `import` uses the user's
own RBAC (`create`, `get`, `update` on `mcpservers`); `export` needs `list`
on `mcpservers` and, for the Operator version, `list` on deployments in the
release namespace (it finds the Operator by its labels); without it the
header says `Operator version: unknown`.

## Documentation

`docs/components/kavryctl/README.md`, a "Rebuilding a cluster" section in
`docs/RELEASE.md` or a new runbook, `ROADMAP.md`.

## Open questions

Resolved when accepted (see [plan.md](plan.md)): `import` creates and
updates rather than server-side applying, so GitOps field managers are not
taken over; `export` includes servers that are not `Ready`.
