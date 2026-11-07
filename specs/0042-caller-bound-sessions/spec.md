# SPEC-0042: MCP sessions and streams bound to their caller

| Field | Value |
| --- | --- |
| Status | Accepted |
| Owners | @Ravi041 |
| Created | 2026-11-02 |
| Issue | none: tracked privately, see [SECURITY.md](../../SECURITY.md) |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

With MCP client authentication on, the Gateway ends an event stream when the token
that opened it expires, and only the caller that started an MCP session can use it.

## Problem

Found by reading the code, not yet by a test against a running Gateway:

- The Gateway checks a token when a request starts. A `GET` event stream then stays
  open for as long as the client does, so a caller whose token expired, or whose
  access was removed at the issuer, keeps receiving the server's messages.
- `Mcp-Session-Id` is forwarded without any link to who started the session. Any
  caller with a valid token for the route that learns another caller's session ID
  can use that session, if the server does not check this itself. The MCP
  specification says servers should bind session IDs to user information, and the
  Gateway replaces the token, so a server often cannot tell callers apart.

## Goals

- Access to a stream ends no later than the token that granted it.
- A session ID is useless to any other caller, on every Gateway replica, with no
  shared state.

## Non-goals

- Revoking a token before its `exp`. It needs introspection or a revocation feed.
- Ending a `POST` response stream at expiry: it answers one call the token allowed.
- Revoking console cookies at sign-out. Replicas share no state and the Gateway has
  no write access to store any; SECURITY.md documents it.
- Rate or connection limits per client, which belong at the ingress.
- Routes with client authentication off, which have no caller to bind to.

## Requirements

### Streams

- **SPEC-0042-R01** With client authentication on, the Gateway MUST end a `GET` event
  stream by closing the response no later than the clock skew after the token that
  opened it expires, with no further grace period.
- **SPEC-0042-R02** A stream ended this way MUST be counted with a new outcome
  `token_expired`, and its audit event MUST have that outcome.
- **SPEC-0042-R09** `token_expired` MUST NOT count as a failure in the chart's alerts
  or the console's alerts. It is the expected end of a stream.

### Sessions

- **SPEC-0042-R03** With client authentication on, the Gateway MUST put in front of
  each `Mcp-Session-Id` it returns a tag that a key only the Gateway has computes over
  the caller's issuer, subject, and client ID, the route, and the session ID.
- **SPEC-0042-R04** The Gateway MUST answer `404`, counted as `session_not_found`,
  without contacting the server, when a request's session ID has no tag or a tag that
  does not match its caller and route, so a caller cannot tell another's session from
  one that does not exist.
- **SPEC-0042-R05** The server MUST get its own session ID, without the tag, and this
  MUST work together with the pod tag of session affinity (SPEC-0026).
- **SPEC-0042-R08** The Gateway MUST check the tag of R03 before it reads the pod tag,
  and the tag MUST cover the pod tag, so a request that fails R04 has no effect on which
  pod is chosen.
- **SPEC-0042-R10** The tag MUST start with a format version, and the Gateway MUST
  answer a version it does not know as R04 does.
- **SPEC-0042-R11** The Gateway MUST refuse with `401` a token that has neither a
  subject nor a client ID, because it could not tell such callers apart.
- **SPEC-0042-R06** The chart MUST generate the key once into a Secret the kubelet
  mounts, keep it across `helm upgrade`, and accept an existing Secret. The Gateway
  gets no RBAC on Secrets, and MUST NOT start with client authentication on without
  the key.
- **SPEC-0042-R07** The key and the tag's inputs MUST NOT appear in logs, metrics,
  audit events, or responses, other than the tag itself.

## Acceptance criteria

- Given a `GET` stream opened with a token that expires in 5 seconds, then the stream
  ends within the clock skew after that, and the client can reconnect with a new token
  and `Last-Event-ID` (R01, R02).
- Given caller A's session ID, when caller B sends it with a valid token for the route,
  then B gets `404` and the server gets nothing (R04).
- Given caller A on a new token with the same issuer, subject, and client ID, then the
  session keeps working (R03).
- Given one user's two agents, with the same subject and different client IDs, then
  neither can use the other's session (R03, R04).
- Given caller A's session ID on route `a`, when A sends it to route `b`, then A gets
  `404` and `b`'s server gets nothing (R03, R04).
- Given a session with a pod tag and a changed pod tag, then the client gets `404` and
  no pod gets the request (R05, R08).
- Given a token with neither `sub` nor a client ID, then the client gets `401` (R11).
- Given a Gateway rolling restart, then sessions keep working (R06). Given an upgrade
  to the first version with this spec, then each session gets `404` once and starts
  again.
- Given a request through another Gateway replica, then the session keeps working
  (R03, R06).
- Given many streams ended at expiry, then no alert fires (R09).

## Security and privacy

Session IDs and tokens are untrusted. The tag fails closed: anything that does not
match gets `404`. A tag without a key would not help, since a caller with a stolen ID
could compute its own. Rotating the key ends every session, which clients already
handle as `session_not_found`. The route in the tag matters most with the Gateway
audience (SPEC-0031), where one token reaches many routes. A render without cluster
access (`helm template`, GitOps) cannot look up the Secret and the chart cannot detect
this, so it would make a new key on each render and end every session. Such installs
must set an existing Secret, as they already do for the console login.

## Compatibility

Sessions started before the upgrade get `404` once and start again, as MCP clients
must already handle, and so do sessions that move between old and new replicas during
the upgrade or a rollback. Long-lived `GET` streams now end at token expiry, so clients
must reconnect with a fresh token; messages sent before they do are lost unless the
server supports resuming with `Last-Event-ID`. A token with neither `sub` nor a client
ID stops working on `/mcp/`. A client that gets a new client ID loses its sessions.
New Secret, Helm value, and outcome. No CRD change.

## Documentation

Gateway component doc (authentication, session affinity, metrics), chart README,
`docs/E2E.md`, SECURITY.md, a dashboard or alert note for the new outcome.

## Decisions

Decided by the owner on 2026-11-04:

- R03: the caller is the issuer, subject, and client ID. One user's agents can have
  different levels of trust, and a client whose ID changes only starts a new session.
- R03: the tag includes the route.
- R01: a stream ends at expiry plus the existing clock skew, with no grace period.
- R01, R02, and R09 are built first because they need no Secret.

Decided by the owner on 2026-11-07:

- This spec ships before the next release, which waits for it (it replaces the
  earlier decision to ship it after `1.0.0-beta.6`).
