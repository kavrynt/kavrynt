# SPEC-0030: Console login limits behind a proxy

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-28 |
| Issue | none (security review) |
| ADRs | none |
| Supersedes | none |

## Summary

The console login (SPEC-0005) refuses logins after five failures a minute
from one client address. Behind an Ingress or load balancer every login
comes from the proxy's address, so the limit applies to everyone at once.
This spec counts failures per real client when the Gateway sits behind a
trusted proxy, and keeps the limiter's memory bounded without resetting it.

## Problem

- Behind an Ingress, five failed logins a minute from anyone keep the
  `admin` user from logging in at all.
- When more than 10,000 addresses have failed, the limiter forgets every
  address at once, which also forgets the ones still being limited.

## Goals

- One client's failures limit that client, also behind a proxy.
- The limiter's memory stays bounded and never forgets a limited client
  early.

## Non-goals

- More users, roles, or SSO for the console.
- Rate limits on MCP routes.

## Requirements

- **SPEC-0030-R01** The chart value `gateway.console.auth.trustedProxies`
  MUST list CIDRs whose `X-Forwarded-For` the Gateway trusts; it is empty
  by default.
- **SPEC-0030-R02** For a request from a trusted proxy, the login limiter
  MUST count failures per the right-most `X-Forwarded-For` address that is
  not itself a trusted proxy; for other requests, per the connection's
  address, as today.
- **SPEC-0030-R03** When the limiter tracks its maximum number of clients,
  it MUST forget the clients whose window has ended first, then the oldest,
  and MUST NOT forget a client that is currently refused.
- **SPEC-0030-R04** The Gateway MUST ignore `X-Forwarded-For` from
  addresses outside `trustedProxies`.

## Acceptance criteria

- Given `trustedProxies: [10.0.0.0/8]` and requests from `10.1.2.3`, when
  client A fails five logins, then A is refused and client B can still log
  in (R01, R02).
- Given no trusted proxies, when a request carries `X-Forwarded-For`, then
  the limiter uses the connection's address (R04).
- Given a full limiter with a refused client, when new clients fail, then
  the refused client stays refused until its window ends (R03).

## Security and privacy

`X-Forwarded-For` is untrusted unless the connection comes from a listed
proxy, and only the right-most untrusted address counts, so a client cannot
pick the address its failures are counted against. Addresses are logged as
today, never passwords.

## Compatibility

A new Helm value in the schema, empty by default, so behavior is unchanged
until it is set. No CRD change.

## Documentation

Gateway component docs (console login), the chart README and values, and
`SECURITY.md`.

## Open questions

- Should the chart fill `trustedProxies` from the Ingress it renders, if
  any? Recommended: no; the Ingress controller's addresses are not known to
  the chart.
