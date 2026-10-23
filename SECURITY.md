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

- The Gateway does not authenticate clients or authorize tool calls yet
  ([ADR-0003](docs/ADR-0003-Runtime-Authorization.md)).
- The web console at `/ui/` has a single `admin` login with a generated
  password. It protects the console, `/v1/servers`, and `/v1/traffic` only:
  `/mcp/` routes, `/v1/routes`, and `/metrics` stay reachable by anyone who
  can reach the Gateway. There are no other users, roles, or SSO.
- There is no audit log and no high-availability profile.

Run the current release only in trusted development or evaluation clusters.

## Verifying releases

Release images and `SHA256SUMS` are signed with Cosign keyless signing from
the `release.yml` workflow. Verification steps:
[docs/RELEASE.md](docs/RELEASE.md#verify).
