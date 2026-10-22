# SPEC-NNNN plan: TITLE

## Design

How the requirements are met: components involved, data flow, and the
decisions a reviewer should check. Keep the component boundaries in
[CLAUDE.md](../../CLAUDE.md#engineering-standards): the Gateway contains no
reconciliation logic, the Operator never proxies traffic, and `kavryctl`
imports only shared API types.

## Affected files

| Path | Change |
| --- | --- |
| `internal/...` | ... |

## Verification

Every requirement maps to at least one test that cites its ID.

| Requirement | Test |
| --- | --- |
| SPEC-NNNN-R01 | `internal/.../x_test.go` `TestName` |
| SPEC-NNNN-R02 | `scripts/e2e-kind.sh` "log message" |

## Rollout

Feature flag or Helm value, default, and how to turn it off. Release notes
entry.

## Risks

What could go wrong and how it is detected.
