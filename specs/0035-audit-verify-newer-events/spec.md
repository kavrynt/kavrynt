# SPEC-0035: Verifying audit events from newer Gateways

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-31 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

`kavryctl audit verify` checks each event's hash on the line as the Gateway
wrote it, so events that carry fields added by a newer Gateway verify with
an older `kavryctl`. When a line was rewritten on its way (a log collector
that reorders members) and holds fields this `kavryctl` does not know, it
says so instead of calling the event altered.

## Problem

`kavryctl audit verify` decodes each event into its own `Event` struct and
re-encodes it to recompute the hash (SPEC-0013-R06, R07). A field the
struct does not have is dropped, so the hash differs and the event is
reported as `hash does not match the event (altered)`. That happened with
`filtered_tools` (SPEC-0016) and happens with `audience` (SPEC-0031): an
operator who verifies a newer Gateway's events with the `kavryctl` they
already have sees every such event as tampered with, which is the one
answer an audit check must not give wrongly. The component docs can only
say "verify with the Gateway's release".

## Goals

- Events from a newer Gateway verify with this and later `kavryctl`
  releases, as long as their lines are unchanged.
- An event that cannot be verified because it has unknown fields is never
  reported as altered.
- Lines re-encoded by collectors keep verifying as they do today.

## Non-goals

- Fixing `kavryctl` releases already published; they keep the old
  behavior.
- Changing the hash or the canonical form (SPEC-0013-R06). The Gateway
  writes and hashes the same bytes as before.
- The Kavrynt Cloud ingestion check, which lives in `kavrynt-cloud`; it can
  use `api/audit/v1.Verify`.

## Requirements

- **SPEC-0035-R01** `Verify` MUST accept an event whose line, with its last
  member removed when that member is `"hash"` with 64 lowercase hex digits,
  hashes with its `prev_hash` (SPEC-0013-R06) to that hash, whatever other
  members the line holds, including members this version does not know.
- **SPEC-0035-R02** When the line does not verify that way, `Verify` MUST
  accept the event when its known fields, re-encoded in the canonical form,
  hash to its hash, as before this spec (lines re-encoded by collectors).
- **SPEC-0035-R03** When neither verifies and the line holds members this
  version does not know, the break MUST say that the event has fields this
  `kavryctl` does not know, name up to five of them, say that it cannot
  tell an edit from a rewrite, and say to verify the original lines with
  the Gateway's release or a newer `kavryctl`; it MUST NOT call the event
  altered, and it stays a break. Without unknown members, the break stays `hash does not match
  the event (altered)`.
- **SPEC-0035-R04** The Gateway MUST write every event as its canonical
  form with the `"hash"` member last, so that R01 holds for every line it
  writes.
- **SPEC-0035-R05** After a run without breaks, `kavryctl audit verify` MUST
  say how many events carried fields it does not know, when there were
  any: their hashes cover those fields, but it did not interpret them.

## Acceptance criteria

- Given a chain written by a Gateway with a field this `kavryctl` does not
  know, when it is verified unchanged, then every chain verifies and the
  output counts the events with unknown fields (R01, R05).
- Given the same chain with one field edited, then that event is a break
  that names the unknown field and does not say altered; given a chain
  without unknown fields and one field edited, then the break says altered
  (R01, R03).
- Given a chain of events without unknown fields whose members a collector
  reordered, then every chain verifies (R02).
- Given an event with an unknown field whose members a collector reordered,
  then its break names the unknown field and the remedy, and does not say
  altered (R03).
- Given any event the Gateway writes, then its line ends with the `"hash"`
  member and verifies by R01 (R04).

## Security and privacy

The hash still covers every byte of the event but the hash member, unknown
members included, so adding, removing, or editing any member of a line is
still detected. The unknown-fields break is only possible for a line that
does not verify; it changes the explanation, not whether it is a break, and
`verify` still exits non-zero. An edited line with unknown fields is
reported this way rather than as altered, because without knowing those
fields the verifier cannot tell an edit from a collector's rewrite; the
Gateway's release, which knows them, reports it as altered. Unknown field names are printed (bounded in
number and length), never their values. As before, the chain is
tamper-evident, not tamper-proof (`SECURITY.md`).

## Compatibility

No change to the event format, the hash, or the Gateway's output. Older
`kavryctl` releases keep reporting newer events as altered; the docs say to
use this release or later.

## Documentation

Gateway component docs (Audit events: verifying), `docs/components/kavryctl`,
`CLAUDE.md`.

## Open questions

Resolved when accepted (see [plan.md](plan.md)): verify on the written line
first, then fall back to re-encoding; name unknown fields rather than only
counting them.
