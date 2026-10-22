# Governance

This document describes how decisions are made in the Kavrynt runtime project.

## Roles

| Role | Who | Can |
| --- | --- | --- |
| Contributor | Anyone who opens an issue, reviews, or sends a pull request | Propose changes and review |
| Reviewer | Contributors with a record of quality reviews in an area | Approve pull requests in that area |
| Maintainer | People listed in [MAINTAINERS.md](MAINTAINERS.md) | Merge, release, and decide on project direction |

## Decision making

- Most decisions are made in pull requests by lazy consensus: a change merges
  when a maintainer approves it and no maintainer objects.
- Changes to the `MCPServer` API, the security model, or the architecture need
  an Architecture Decision Record in `docs/` and approval from a majority of
  maintainers.
- If consensus cannot be reached, maintainers vote. A change passes with a
  simple majority of maintainers, and a tie keeps the status quo.
- Changes to this document need a two-thirds majority of maintainers.

## Becoming a maintainer

A contributor can be nominated by any maintainer after sustained, high-quality
contributions (code, review, docs, or release work) over at least three
months. Nomination is a pull request adding them to `MAINTAINERS.md`; it
passes with a majority of maintainers and no veto from more than one of them.

The project aims to have maintainers from more than one organization. No
single organization should hold a majority of maintainer seats once the
project has at least five maintainers.

## Stepping down and removal

Maintainers who are inactive for six months are moved to emeritus status after
being contacted. A maintainer can be removed for a Code of Conduct violation by
a two-thirds majority of the other maintainers.

## Security response

Vulnerability reports follow [SECURITY.md](SECURITY.md). Maintainers handle
them privately until a fix is released.

## Releases

Releases follow [docs/RELEASE.md](docs/RELEASE.md). Any maintainer can cut a
release once every quality gate passes.
