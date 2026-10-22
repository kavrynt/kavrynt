# ADR-0004: License The Runtime Under Apache-2.0

## Status

Accepted (2026-10-20). Applies from the first release after `0.0.4-beta.1`.

## Context

The runtime was published under the Elastic License 2.0 (ELv2), a
source-available license. ADR-0001 recorded the `LICENSE` file as an open
owner and legal decision.

The project intends to build an open community and to apply to the CNCF
Sandbox. CNCF requires projects to be licensed under Apache-2.0 before
acceptance; ELv2 is not an OSI-approved license and is not accepted.

At the time of this decision, every human-authored commit in the repository
was written by the owner, so no other contributor's consent is needed to
relicense.

## Decision

License the `kavrynt` runtime repository under the Apache License, Version
2.0.

- `LICENSE` contains the unmodified Apache-2.0 text; `NOTICE` carries the
  copyright line.
- Images carry `org.opencontainers.image.licenses="Apache-2.0"`.
- Contributions are accepted under Apache-2.0 with a Developer Certificate of
  Origin sign-off (`CONTRIBUTING.md`).
- Hosted features stay in `kavrynt-cloud`, which this decision does not
  change.

## Consequences

- Releases up to and including `0.0.4-beta.1` remain under ELv2. Later
  releases are Apache-2.0.
- The runtime can be described as open source. Docs, the README, and
  `product-docs` must say Apache-2.0, not source-available.
- Anyone, including hosted competitors, can run and redistribute the runtime.
  Commercial differentiation comes from `kavrynt-cloud`.
- If the project joins the CNCF, the project name and its trademark are
  transferred to the Linux Foundation. Whether the project keeps the
  "Kavrynt" name is a separate decision.
