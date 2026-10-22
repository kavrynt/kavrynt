# SPEC-0001 plan: Gateway web console

Written after the fact: this records how the shipped console meets its
requirements.

## Design

- `internal/gateway/console` embeds `static/` (plain HTML, CSS, and
  JavaScript, no build step) and serves it with fixed security headers.
- `internal/gateway/api` mounts the console under `/ui/` when
  `EnableConsole` is called, and serves `/v1/servers` from the route table and
  `/v1/traffic` from an in-memory ring of 500 records per replica.
- The browser polls `/v1/servers` and `/readyz` every 5 seconds and
  `/v1/traffic` every 2 seconds. The playground calls `/mcp/<route>` on the
  same origin, so it passes the same Origin check, header stripping, and
  egress rules as any client.
- DOM nodes are built with a small `h()` helper that only sets
  `textContent` and attributes.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/console/` | Static console and its handler |
| `internal/gateway/api/api.go` | `/ui/`, `/v1/servers`, `/v1/traffic` routes |
| `internal/gateway/api/traffic.go` | Recent traffic ring |
| `cmd/gateway` | `--console` flag |
| `charts/kavrynt/charts/gateway` | `console.enabled` value |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0001-R01 | `internal/gateway/api/security_test.go` `TestConsole`; `scripts/e2e-kind.sh` "verifying the console" |
| SPEC-0001-R02 | `internal/gateway/api/security_test.go` `TestConsole` |
| SPEC-0001-R03 | `internal/gateway/console/console_test.go` `TestHandlerAllowsOnlyReads` |
| SPEC-0001-R04 | `internal/gateway/console/console_test.go` `TestHandlerSetsSecurityHeaders`; `internal/gateway/api/security_test.go` `TestConsole` |
| SPEC-0001-R05 | `internal/gateway/console/console_test.go` `TestStaticFilesUseNoHTMLSinks` |
| SPEC-0001-R06 | `internal/gateway/api/security_test.go` `TestServersAndTrafficEndpoints`; `scripts/e2e-kind.sh` |
| SPEC-0001-R07 | `internal/gateway/api/security_test.go` `TestServersAndTrafficEndpoints`, `TestRecentTrafficKeepsNewestFirst`; `scripts/e2e-kind.sh` |
| SPEC-0001-R08 | `internal/gateway/api/security_test.go` `TestServersAndTrafficEndpoints`; `scripts/e2e-kind.sh` |

## Rollout

Shipped enabled by default. Disable with `gateway.console.enabled=false`.

## Risks

- The console is unauthenticated. Mitigated by keeping the Gateway Service
  internal; resolved by ADR-0003.
- `/v1/traffic` is per replica, so with more than one replica the console
  shows only the replica that served the page.
