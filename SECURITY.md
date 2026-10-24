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

Kavrynt is in beta. Only the latest `0.0.x` prerelease receives security
fixes. Fixes are not backported.

## Known limitations

These are documented gaps, not vulnerabilities:

- MCP client authentication (OAuth 2.1 bearer tokens, `gateway.auth`) is off
  by default; until it is turned on, anyone who can reach the Gateway can call
  every routed tool. The Gateway does not authorize individual tool calls yet
  ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)).
- The web console at `/ui/` has a single `admin` login with a generated
  password. It protects the console, `/v1/servers`, and `/v1/traffic` only;
  `/v1/routes` and `/metrics` stay reachable by anyone who can reach the
  Gateway (`/v1/routes` lists no endpoints while MCP client authentication
  is on). There are no other users, roles, or SSO.
- There is no audit log.

Run the current release only in trusted development or evaluation clusters.

## Verifying releases

Release images and `SHA256SUMS` are signed with Cosign keyless signing from
the `release.yml` workflow. Verification steps:
[docs/RELEASE.md](docs/RELEASE.md#verify).
