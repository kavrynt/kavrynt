# SPEC-0034: Registration export and import

| Field | Value |
| --- | --- |
| Status | Draft |
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
  else, in a stable order (namespace, then name).
- **SPEC-0034-R02** The file MUST begin with a comment naming the export
  time, the source context's cluster server, the Operator version, and,
  for each server with `spec.credential`, the upstream credentials Secret
  keys it needs (`<namespace>.<name>`, `<namespace>.<name>.origin`). It
  MUST NOT contain Secret data and MUST NOT read Secrets.
- **SPEC-0034-R03** Every document MUST pass `kavryctl validate`.

### Import

- **SPEC-0034-R04** `kavryctl import FILE` MUST validate every document
  first and change nothing when any is invalid.
- **SPEC-0034-R05** For each document, `import` MUST create the
  `MCPServer` when it does not exist, leave it alone when its `spec` is
  equal, and refuse it when its `spec` differs, unless `--overwrite` is
  given. Refused documents MUST be listed, and the exit code MUST be `1`.
- **SPEC-0034-R06** `--dry-run` MUST report what `import` would create,
  leave, refuse, or overwrite, using server-side dry run, and change
  nothing.
- **SPEC-0034-R07** `import` MUST NOT create namespaces. A document whose
  namespace does not exist MUST be refused with a message naming it.
- **SPEC-0034-R08** After applying, `import` MUST wait up to `--wait`
  (default `60s`, `0` to skip) for each imported server's conditions and
  report the servers that are not `Ready`, with their reason, and those
  whose `ToolsDiscovered` is `False` with reason `CredentialUnavailable`,
  naming the upstream credentials Secret keys to provide.

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
  refuses it, changes nothing for it, and exits `1`; with `--overwrite` it
  replaces it (R05).
- Given `--dry-run`, then nothing changes (R06).
- Given a file with one invalid document, then nothing is applied (R04).

## Security and privacy

The export holds endpoints and labels, which can name internal services;
the documentation says to store it like other cluster configuration. It
never holds Secret data, and neither command reads Secrets. `import` applies
untrusted files only after the same validation as `kavryctl register`, and
admission (CRD validation, endpoint scope SPEC-0029) still applies.

## Compatibility

New `kavryctl` commands; no CRD or chart change. `import` uses the user's
own RBAC (`create`, `get`, `update` on `mcpservers`).

## Documentation

`docs/components/kavryctl/README.md`, a "Rebuilding a cluster" section in
`docs/RELEASE.md` or a new runbook, `ROADMAP.md`.

## Open questions

- Should `import` use server-side apply with a `kavryctl` field manager
  instead of create and update? Recommended: create and update, so GitOps
  field managers are not taken over by a restore. The owner decides.
- Should `export` include `MCPServer`s that are not `Ready`? Recommended:
  yes; readiness depends on the cluster, not the registration.
