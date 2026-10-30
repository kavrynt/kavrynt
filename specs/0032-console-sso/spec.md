# SPEC-0032: Console sign-in through the authorization server

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-30 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

People sign in to the web console with the organization's OpenID Connect
provider, the same one that issues MCP access tokens, instead of sharing the
generated `admin` password. Only the users and groups the platform team
lists get a console session. The `admin` login can stay as a break-glass
login or be turned off.

## Problem

The console has one shared `admin` login (SPEC-0005). Everyone who uses it
shares one password, nobody's access can be removed without rotating it for
everyone, and the console cannot tell people apart. Platform teams that
evaluate Kavrynt for production expect single sign-on, and `SECURITY.md`
lists the missing SSO as a known limitation.

## Goals

- Console sign-in with an OpenID Connect provider (Authorization Code flow
  with PKCE), configured in the chart.
- Access only for listed subjects or groups.
- A console session that identifies the person, with the same cookie
  protections as today.
- The `admin` login off when SSO is on, unless the platform team keeps it
  as a break-glass login.

## Non-goals

- Roles inside the console. The console is read-only; everyone allowed in
  sees the same pages.
- Protecting `/metrics` and `/v1/routes`, which the console login does not
  protect today either (a separate change).
- MCP client authentication (SPEC-0006): console sessions never authorize
  `/mcp/` calls, and MCP access tokens never open a console session.
- Single logout at the provider (RP-initiated or back-channel logout).
- SAML.

## Requirements

### Configuration

- **SPEC-0032-R01** The chart value `gateway.console.auth.oidc.enabled`
  MUST default to `false`. When `true`, the chart MUST refuse to render
  without `issuer`, `clientID`, `clientSecret` (a Secret name and key in the
  release namespace), `redirectURL`, and at least one of `allowedSubjects`
  or `allowedGroups`. `issuer` and `redirectURL` MUST be `https`, except
  `issuer` with `allowHTTP: true`, as in SPEC-0006-R20. `providerName`
  (default `your identity provider`) names the provider on the sign-in
  button.
- **SPEC-0032-R02** The Gateway MUST read the client secret from the
  kubelet-mounted Secret, never from an argument or environment variable,
  and MUST NOT get RBAC on Secrets.

### Sign-in

- **SPEC-0032-R03** With SSO on and no valid session, the console MUST
  offer "Sign in with <providerName>". Choosing it MUST redirect to the
  provider's authorization endpoint, from its metadata, which MUST be
  `https` even when the issuer sets `allowHTTP`: the browser reaches it, so
  `allowHTTP` covers only the Gateway's own requests (metadata, keys, token
  endpoint). Otherwise the Gateway MUST refuse to start the sign-in. The
  redirect carries `response_type=code`, the
  configured `client_id`, `redirect_uri`, scopes `openid` plus the
  configured ones, a random `state`, a random `nonce`, and a PKCE `S256`
  `code_challenge`.
- **SPEC-0032-R04** `state`, `nonce`, and the PKCE verifier MUST be kept in
  a signed, `HttpOnly`, `SameSite=Lax` cookie scoped to the callback path
  that expires after 10 minutes and is cleared by the callback, whatever its
  outcome.
- **SPEC-0032-R05** The callback MUST refuse (no session, `400`) a missing
  or mismatched `state`, a provider error, and a missing code. It MUST
  exchange the code at the provider's token endpoint with
  `client_secret_basic` and the PKCE verifier, through the egress guard.
- **SPEC-0032-R06** The Gateway MUST verify the ID token's signature,
  issuer, and times with the rules of SPEC-0006-R08 (asymmetric algorithms
  only, key from the issuer's key set, `iss` exact, `exp` and `iat` with
  60 s leeway), but not its audience rule. Instead `aud` MUST contain the
  client ID; when `aud` has other values too, `azp` MUST equal the client
  ID (OpenID Connect Core 3.1.3.7). `nonce` MUST match. Any failure MUST
  refuse the sign-in.
- **SPEC-0032-R07** A verified user MUST get a session only when their
  `sub` is in `allowedSubjects` or a value of the configured groups claim
  (default `groups`) is in `allowedGroups`. Others MUST get `403` with a
  page that says they are not allowed, and no session.
- **SPEC-0032-R08** The session MUST have the cookie properties of
  SPEC-0005-R05, last at most `gateway.console.auth.sessionTTL`, and name
  the person by the configured display claim (default
  `preferred_username`, falling back to `sub`).
- **SPEC-0032-R09** The Gateway MUST NOT keep, log, or return the
  authorization code, the access, ID, or refresh token, or the client
  secret. Refused sign-ins MUST be logged with a reason and never with
  those values.

### The admin login

- **SPEC-0032-R10** With SSO on, `POST /v1/session` with the `admin`
  password MUST answer `404` unless
  `gateway.console.auth.oidc.keepAdminLogin` is `true`; with it `true`, the
  admin login MUST keep working, and the Gateway MUST log at start that a
  shared password still opens the console.
- **SPEC-0032-R11** `GET /v1/session` MUST report the sign-in method
  (`oidc` or `admin`) and the signed-in name, and the console MUST show it,
  inserted as text.

## Acceptance criteria

- Given default values, then the console uses the `admin` login as today
  (R01).
- Given SSO with `providerName: Keycloak`, `allowedGroups: [platform]`, and
  a user in `platform`, when they choose "Sign in with Keycloak" and
  authenticate, then they return to
  the console signed in under their username (R03 to R08, R11).
- Given a user in no listed group, when they authenticate, then they get
  `403` and no session (R07).
- Given a callback with a `state` from another browser, or an ID token with
  another `nonce` or `aud`, or with the client ID and another audience but
  no matching `azp`, then the sign-in is refused (R05, R06).
- Given an issuer whose metadata names an `http` authorization endpoint,
  with or without `allowHTTP`, then the console does not redirect to it
  (R03).
- Given SSO on without `keepAdminLogin`, when someone posts the admin
  password, then they get `404` (R10).
- Given a refused sign-in, then the Gateway's logs contain its reason and
  no code, token, or secret (R09).

## Security and privacy

The callback takes untrusted query parameters and provider responses; every
check fails closed. PKCE, `state`, and `nonce` defeat code interception,
login CSRF, and ID token replay. The client secret and provider requests
follow the patterns of token exchange (SPEC-0015): kubelet-mounted Secret,
egress guard, bounded reads, no redirects followed. The session cookie now
carries a person's name; it stays signed, `HttpOnly`, and scoped to
`/v1/`. Keeping the admin login is an explicit choice and is logged.

## Compatibility

New values, off by default; installations that do not set them keep the
`admin` login. No CRD change. The authorization server needs a
confidential client with the console's redirect URL.

## Documentation

Gateway component docs (Console login), `docs/AUTH-SETUP.md` (Keycloak
client for the console), chart README and values, `SECURITY.md` (remove "no
SSO" from the known limitations), `ROADMAP.md`.

## Open questions

- Should SSO reuse the issuer list of `gateway.auth.issuers`, or have its
  own `issuer`? Recommended: its own, since the console may use another
  provider or realm than agents. The owner decides.
- Should `allowedGroups` also gate `/v1/alerts` and `/v1/history` per
  group, for example per namespace? Recommended: not now (non-goal: roles).
