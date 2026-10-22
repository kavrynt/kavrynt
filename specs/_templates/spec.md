# SPEC-NNNN: TITLE

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @github-id |
| Created | YYYY-MM-DD |
| Issue | #000 |
| ADRs | none |
| Supersedes | none |

## Summary

One paragraph: the change, who it is for, and the outcome.

## Problem

What users, operators, or clients cannot do today, and why it matters. Link
the issue and any evidence.

## Goals

- Outcome this spec delivers.

## Non-goals

- Related work this spec deliberately leaves out, and where it belongs
  instead (another spec, an ADR, or Kavrynt Cloud).

## Requirements

<!-- One observable behavior per requirement. RFC 2119 keywords. Never renumber. -->

- **SPEC-NNNN-R01** The COMPONENT MUST ...
- **SPEC-NNNN-R02** The COMPONENT MUST NOT ...

## Acceptance criteria

<!-- How a reviewer checks the requirements end to end, in Given/When/Then form. -->

- Given ..., when ..., then ... (SPEC-NNNN-R01)

## Security and privacy

What untrusted input this touches, what new access or exposure it adds, and
how it fails closed. Tool arguments, results, and secrets must never be
logged, stored, or displayed beyond what the requirements state.

## Compatibility

Effects on the `MCPServer` API, Helm values, CLI flags, and upgrades from the
previous release. State whether a CRD change is needed.

## Documentation

Which docs change once this is `Implemented` (README, `docs/components/`,
chart README, `ROADMAP.md`).

## Open questions

- Question, and who decides.
