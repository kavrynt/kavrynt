# SPEC-0035 plan: Verifying audit events from newer Gateways

## Decisions

- Verify the written line first, then fall back to re-encoding the known
  fields (open question 1). The written line keeps every member a newer
  Gateway added; the fallback keeps lines that collectors re-encoded
  working as before.
- Name the unknown fields (up to five, quoted, at most 64 characters each)
  rather than only counting them, so the operator knows which release to
  verify with.
- An event with unknown fields that does not verify is a break that says
  it cannot tell an edit from a rewrite, not "altered": without knowing
  the fields, the verifier cannot re-encode the line. The Gateway's own
  release still reports an edit as altered.
- Numbered 0035 by hand: 0032 to 0034 are taken by drafts in another open
  pull request.

## Design

- **Written line (R01, R04).** The Gateway marshals `Event` with `Hash`
  last (`hash,omitempty`, the last field), so a written line is the
  canonical form with `,"hash":"<hex>"` before its closing brace.
  `verifiesAsWritten` strips that member (64 lowercase hex digits, the
  last member only) and hashes `prev_hash` plus the rest, the bytes the
  Gateway hashed.
- **Fallback (R02).** `ComputeHash` of the decoded known fields, as before.
- **Unknown fields (R03, R05).** `unknownFields` decodes the line's
  top-level members and keeps the names that are not `Event`'s JSON tags
  (read once by reflection). `Summary.UnknownFields` counts events with
  any; `kavryctl audit verify` prints the count after a clean run.

## Affected files

| Path | Change |
| --- | --- |
| `api/audit/v1/verify.go` | Written-line check, fallback, unknown fields |
| `internal/kavryctl/cli/cli.go` | Count of events with unknown fields |
| Docs | Gateway component docs (Audit events), kavryctl docs, `CLAUDE.md` |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `auditv1.TestVerifyAcceptsNewerEventsAsWritten`, `auditv1.TestUnknownFieldsAreNotCalledAltered` |
| R02 | `auditv1.TestVerifyFallsBackToReencoding` |
| R03 | `auditv1.TestUnknownFieldsAreNotCalledAltered` |
| R04 | `audit.TestWriterPutsTheHashLast` |
| R05 | `auditv1.TestVerifyAcceptsNewerEventsAsWritten`, `cli.TestAuditVerify` |
| R06 | `auditv1.TestAddedFieldsAreNotTrustedByReencoding` |

## Rollout

A `kavryctl` change only; the Gateway's output is unchanged. Release
notes: "`kavryctl audit verify` verifies events from newer Gateways and no
longer calls them altered (SPEC-0035)."

## Risks

- A collector that rewrites lines with the hash still last but other bytes
  changed (whitespace, escaping) fails the written-line check and falls
  back to re-encoding; with unknown fields, it is a break that names them.
- The unknown-fields break is weaker evidence than "altered" for an edited
  line with unknown fields; it is still a break and a non-zero exit.
