# SPEC-0037 plan: kavryctl list points to other namespaces

## Decisions

- Numbered 0037 by hand: SPEC-0036 is in an open pull request (#118).

## Design

In `runList`, when the namespaced list is empty, `emptyListMessage` counts
`MCPServer`s cluster-wide in pages of 500, keeping only the count, within
5 seconds. A count above zero is added to the message; any error (for
example `forbidden`) or timeout leaves the count out. The suggested
`kavryctl list -A` keeps the user's `--kubeconfig` and `--context`
(shell-quoted). `-A` keeps the old message.

## Affected files

| Path | Change |
| --- | --- |
| `internal/kavryctl/cli/cli.go` | `emptyListMessage` |
| `internal/kavryctl/cli/cli_test.go` | New test |
| `docs/components/kavryctl/README.md` | The empty-list message |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `cli.TestEmptyListPointsToOtherNamespaces` |
| R02 | `cli.TestEmptyListPointsToOtherNamespaces` (a count; a client that refuses the cluster-wide list) |
| R03 | `cli.TestEmptyListPointsToOtherNamespaces` |

## Rollout

A message change; no flag.

## Risks

None beyond one extra list request when the namespace is empty.
