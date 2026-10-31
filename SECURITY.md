# Security Policy

## Reporting a vulnerability

Do not open a public issue for a security problem.

Report it privately through
[GitHub private vulnerability reporting](https://github.com/kavrynt/kavrynt/security/advisories/new).
Include the affected version, the component (`gateway`, `operator`,
`kavryctl`, or the Helm chart), reproduction steps, and the impact you
observed.

The maintainers aim to acknowledge a report within 3 business days and to
share an assessment and a fix plan within 10 business days. Fixes ship in the
next prerelease, and the advisory is published once a fixed version is
available. Reporters are credited unless they ask not to be.

## Supported versions

Kavrynt is in beta. Only the latest prerelease (`1.0.0-beta.N`) receives
security fixes. Fixes are not backported.

## Known limitations

These are documented gaps, not vulnerabilities:

- MCP client authentication (OAuth 2.1 bearer tokens, `gateway.auth`) is on
  by default in the chart, which refuses to install until it names an
  authorization server. An install that sets `gateway.auth.enabled=false`
  lets anyone who can reach the Gateway call every routed tool. Authorizing
  individual tool calls takes the opt-in policy (`gateway.policy`); without
  it, every authenticated caller can call every tool
  ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)).
- The web console at `/ui/` has a single `admin` login with a generated
  password. It protects the console, `/v1/servers`, `/v1/traffic`,
  `/v1/alerts`, and `/v1/history` only; `/v1/routes` and `/metrics` stay
  reachable by anyone who can reach the Gateway (`/v1/routes` lists no
  endpoints while MCP client authentication is on). Sign-in through your
  identity provider (`gateway.console.auth.oidc`) replaces the shared
  password with listed users and groups; there are no roles within the
  console.
- The opt-in Gateway audience (`gateway.auth.gatewayAudience`) lets one
  token reach every listed route, so a leaked token for it reaches all of
  them. It is off by default, refuses `*`, and is visible in policy input,
  audit events, the start-up log, and the console.
- Audit events (`gateway.audit`) are tamper-evident, not tamper-proof: the
  hash chain detects a removed, reordered, or edited event unless every later
  event is recomputed as well. Ship them out of the cluster to protect them.

Run the current release only in trusted development or evaluation clusters.

## Verifying releases

Release images and `SHA256SUMS` are signed with Cosign keyless signing from
the `release.yml` workflow. Verification steps:
[docs/RELEASE.md](docs/RELEASE.md#verify).
