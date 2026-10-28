# SPEC-0030 plan: Console login limits behind a proxy

## Decisions

- The chart does not fill `trustedProxies` from its Ingress (open question,
  as recommended): the Ingress controller's addresses are not known to it.
- When the limiter is full of clients it currently refuses, it cannot make
  room without forgetting one (R03). Further clients then share one
  overflow entry, so their failures still count, five a minute together.
  This fails closed: during such an attack, new clients may be refused
  until the window ends, but an attacker with more than 10,000 addresses
  gets no unlimited attempts.

## Design

- **Values (R01).** `gateway.console.auth.trustedProxies`, a list of CIDRs
  (empty by default), validated by the schema and passed as
  `--console-trusted-proxies` (comma-separated). The Gateway refuses to
  start with a CIDR it cannot parse.
- **Client address (R02, R04).** `auth.Authenticator.TrustProxies(prefixes)`.
  `clientAddress` takes the connection's address; when it is in a trusted
  prefix, it walks the `X-Forwarded-For` values (all headers, joined) from
  the right and takes the first address that is not trusted. A value that
  is not an IP address ends the walk, and the connection's address is used,
  so a malformed header counts against the proxy rather than letting the
  client choose. Requests from other addresses ignore the header.
- **Bounded limiter (R03).** When a new client fails and 10,000 clients are
  tracked, the limiter first drops every client whose window has ended,
  then the oldest client that is not refused; if every tracked client is
  refused, the new one counts in the overflow entry. `blocked` uses the
  same key, so an overflowed client is refused when the overflow entry is.

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `charts/kavrynt/tests/console_auth_test.yaml`, `values_schema_test.yaml` |
| R02 | `auth.TestLoginLimitBehindTrustedProxies` |
| R03 | `auth.TestLimiterNeverForgetsRefusedClients` |
| R04 | `auth.TestForwardedForIsIgnoredFromUntrustedAddresses` |

## Rollout and risks

- No change until `trustedProxies` is set.
- Listing a range clients can send from (for example the whole Pod range
  while clients run in the cluster) lets those clients choose their
  address; the docs say to list only the proxies.
