# SPEC-0005: Console admin login

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-23 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) (this spec is not ADR-0003 client authentication) |
| Supersedes | none |

## Summary

A single admin login for the Gateway web console, in the style of Argo CD's
initial admin password: the Helm chart generates a random password into a
Secret, the operator reads it with `kubectl`, and the console and the
endpoints it reads require a session. MCP routes are not affected: agents
keep calling `/mcp/` as before.

## Problem

The console has no login. Anyone who can reach the Gateway can browse every
registered MCP server, its endpoints, its tools, and recent traffic. Platform
teams cannot put the console behind an Ingress for their colleagues without
exposing that inventory. Full client authentication (ADR-0003) is a larger
design that also covers agents; phase 1 needs a simple, safe default for
humans using the console.

## Goals

- The console and its data endpoints require a login by default.
- Installing the chart produces a strong admin password without any manual
  step, retrievable with `kubectl`, and stable across upgrades.
- The Gateway keeps its least-privilege RBAC: no access to Secrets.

## Non-goals

- Authenticating MCP clients or protecting `/mcp/` routes. Anyone who can
  reach the Gateway can still call every routed tool. That is ADR-0003.
- More than one user, roles, SSO, or OIDC.
- Changing the password from the console or a CLI. Phase 1 rotates it by
  updating the Secret.
- Audit logging of console use.

## Requirements

### Protected endpoints

- **SPEC-0005-R01** When console login is enabled, `GET /v1/servers` and
  `GET /v1/traffic` MUST answer `401` with a JSON error unless the request
  carries a valid session.
- **SPEC-0005-R02** Console login MUST NOT change the behavior of `/mcp/`
  routes, `/v1/routes`, `/healthz`, `/readyz`, `/version`, or `/metrics`.
- **SPEC-0005-R03** When console login is disabled, every endpoint MUST
  behave as before this spec.

### Login and sessions

- **SPEC-0005-R04** `POST /v1/session` with a JSON body holding username
  `admin` and the admin password MUST create a session and answer `204`;
  any other username or password MUST answer `401` with the same error for
  both. The password MUST be compared in constant time.
- **SPEC-0005-R05** The session MUST be an `HttpOnly`, `SameSite=Strict`
  cookie scoped to `/v1/`, signed by the Gateway, and expire after the
  configured session lifetime (default 12 hours). It MUST be `Secure` when
  the request arrived over HTTPS directly or as reported by
  `X-Forwarded-Proto: https`.
- **SPEC-0005-R06** Changing the admin password MUST invalidate every
  existing session without restarting the Gateway.
- **SPEC-0005-R07** `GET /v1/session` MUST report whether login is enabled
  and, for a valid session, the username and expiry; `DELETE /v1/session`
  MUST end the session in the browser.
- **SPEC-0005-R08** `POST /v1/session` MUST accept only
  `Content-Type: application/json` and MUST stay subject to the Gateway's
  origin check.
- **SPEC-0005-R09** After five failed logins from one client address within
  a minute, the Gateway MUST answer further logins from that address with
  `429` and `Retry-After` until the minute has passed, even with the
  correct password.
- **SPEC-0005-R10** The Gateway MUST NOT log or return the admin password
  or session tokens; it MUST log failed and successful logins with the
  client address.

### Password provisioning

- **SPEC-0005-R11** The Gateway MUST read the admin password and the
  session signing key from files in a directory (a mounted Secret). It MUST
  refuse to start with login enabled and no directory, and MUST answer
  logins with `503` while the files are missing or empty.
- **SPEC-0005-R12** By default the chart MUST enable console login and
  create a Secret with a random 32-character admin password and a random
  64-character signing key, keep both across `helm upgrade`, and keep the
  Secret on `helm uninstall`.
- **SPEC-0005-R13** The chart MUST accept an existing Secret
  (`console.auth.existingSecret`) instead of creating one, and MUST mount
  the Secret read-only without granting the Gateway any RBAC on Secrets.

### Console

- **SPEC-0005-R14** When login is enabled and there is no valid session,
  the console MUST show a login form and no server, tool, or traffic data;
  when a session expires, the console MUST return to the login form.
- **SPEC-0005-R15** The console MUST show the signed-in user and a way to
  log out, and MUST NOT store the password anywhere but the form field.
- **SPEC-0005-R16** With login enabled, the notice of SPEC-0002-R12 MUST
  instead state that MCP routes are not authenticated: anyone who can reach
  the Gateway can call every routed tool.

## Acceptance criteria

- Given a fresh `helm install`, when an operator opens the console, then it
  shows a login form; the password from
  `kubectl -n kavrynt-system get secret kavrynt-gateway-console-admin -o jsonpath='{.data.password}' | base64 -d`
  signs them in (R01, R04, R12, R14).
- Given a signed-in console, when the Secret's password is changed and the
  kubelet has updated the mount, then the next poll returns the console to
  the login form (R06, R14).
- Given six wrong passwords in a minute, when the right password is sent,
  then the Gateway answers `429` (R09).
- Given console login is enabled, when an agent calls `/mcp/<route>`
  without any cookie, then the call is proxied as before (R02).
- Given `helm upgrade`, when the release is upgraded, then the password is
  unchanged (R12).
- Given the Gateway's service account, when its RBAC is listed, then it has
  no access to Secrets (R13).

## Security and privacy

The password and signing key reach the Gateway only as files mounted from a
Secret by the kubelet; the Gateway's service account gets no Secret RBAC.
Sessions are signed with the key and bound to a digest of the current
password, so a stolen cookie cannot be used to guess the password offline
and rotating the password ends every session. The cookie is `HttpOnly`,
`SameSite=Strict`, scoped to `/v1/`, and so never sent to `/mcp/` routes
(the Gateway strips `Cookie` upstream regardless). Login requests are JSON
only and keep the origin check, which blocks cross-site form posts and DNS
rebinding. Failed logins are rate limited per client address; behind an
Ingress that address is the Ingress, so the limit is effectively global.

This login does not authenticate MCP clients. The console's Playground calls
`/mcp/` routes, which stay open to anyone who can reach the Gateway; R16
says so on screen, and the docs must say so wherever the login is
described.

## Compatibility

Upgrading turns the login on: operators need the generated password to use
the console after upgrading, and scripts that read `/v1/servers` or
`/v1/traffic` must sign in or set `gateway.console.auth.enabled=false`. New
Helm values: `gateway.console.auth.enabled`, `.existingSecret`, and
`.sessionTTL`. New Gateway flags: `--console-auth`, `--console-auth-dir`,
`--console-session-ttl`. No CRD change. Installs that render the chart
without cluster access (`helm template`, GitOps) cannot reuse the generated
password across renders and should set `existingSecret`.

## Documentation

When `Implemented`: the Gateway component doc (endpoints, flags, a Console
login section that states what it does not protect), the chart README
values and security notes, the README "Open the console" step, and
`docs/DEMO.md`.

## Open questions

- None for phase 1. Password change through a CLI and multiple users belong
  to a later spec.
