# SPEC-0037: kavryctl list points to other namespaces

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-31 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

When `kavryctl list` finds no `MCPServer` in the current namespace, it says
which namespace it looked in, how many servers other namespaces hold when
it may count them, and how to list them.

## Problem

In a real-environment test with servers registered in team namespaces,
`kavryctl list` from the `default` namespace printed `no MCPServers found`,
which reads as "registration failed", although `kavryctl list -A` showed
all three. Teams register servers in their own namespaces, so the first
`list` a new user runs is often empty.

## Goals

- An empty `list` names the namespace it searched and the way to search
  all of them.

## Non-goals

- Changing which namespace `list` searches by default.

## Requirements

- **SPEC-0037-R01** When `kavryctl list` without `-A` finds no
  `MCPServer`, it MUST print `no MCPServers found in namespace <ns>` and
  name `kavryctl list -A`, with exit code `0`.
- **SPEC-0037-R02** When the user may list `MCPServer`s in all namespaces
  and some exist there, the same line MUST say how many; when the user may
  not, `list` MUST NOT fail or print an error because of it.
- **SPEC-0037-R03** `kavryctl list -A` with no `MCPServer` anywhere MUST
  keep printing `no MCPServers found`.

## Acceptance criteria

- Given three servers in team namespaces and the context in `default`,
  when the user runs `kavryctl list`, then it prints `no MCPServers found
  in namespace default; 3 in other namespaces: kavryctl list -A` (R01, R02).
- Given a user who may list only their namespace, then the line names the
  namespace and `kavryctl list -A`, without a count or an error (R01, R02).
- Given no servers anywhere, `kavryctl list -A` prints `no MCPServers
  found` (R03).

## Security and privacy

The count needs `list` on `mcpservers` across namespaces, the permission
`kavryctl list -A` already uses. Without it, nothing more is shown, and no
names from other namespaces are ever printed.

## Compatibility

The line still starts with `no MCPServers found`, so scripts that match it
keep working.

## Documentation

`docs/components/kavryctl/README.md`.

## Open questions

None.
