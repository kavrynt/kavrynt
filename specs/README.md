# Specifications

Kavrynt uses spec-driven development: a user-visible change is described as a
reviewed specification before it is built, and every requirement in an
implemented specification is proven by a test that names it.

A spec answers **what** the runtime must do and how we will know it does.
An [ADR](../docs/) answers **why** an architectural direction was chosen. A
spec links the ADRs it implements; it does not repeat them.

## When a spec is required

Write a spec before implementing a change to anything a user, operator, or
client can observe:

- the `MCPServer` API, its validation, or its status conditions;
- Gateway endpoints, routing, headers, or security behavior;
- the web console;
- `kavryctl` commands, flags, or output;
- Helm values, chart resources, or RBAC;
- the Kavrynt Cloud connector.

A spec is not needed for bug fixes that restore specified or documented
behavior, refactoring, test-only changes, dependency and toolchain bumps, CI,
or documentation. When unsure, ask in the issue.

## Layout

```text
specs/
├── README.md                 # this process
├── _templates/               # copied by scripts/new-spec.sh
└── NNNN-short-name/
    ├── spec.md               # what and why: requirements, acceptance, non-goals
    ├── plan.md               # how: design, affected files, verification map
    └── tasks.md              # optional: ordered, PR-sized work items
```

Numbers are four digits, assigned in order, and never reused. Create a new
spec with:

```bash
make spec-new NAME=short-kebab-name
```

## Requirements

Each requirement has a stable ID, `SPEC-NNNN-RNN`, and is written in bold at
the start of its list item in `spec.md`:

```markdown
- **SPEC-0007-R01** The Gateway MUST return `401` when ...
```

Use MUST, MUST NOT, SHOULD, and MAY as defined in
[RFC 2119](https://www.rfc-editor.org/rfc/rfc2119). One requirement states one
observable behavior that a test can check. Never renumber a requirement; when
one is dropped, mark it `Withdrawn` in place.

## Traceability

Tests cite the requirements they verify, in a comment next to the check:

```go
// Verifies SPEC-0001-R03.
func TestConsole(t *testing.T) {
```

```yaml
# Verifies SPEC-0003-R03.
- it: rejects an origin with a path
```

```bash
# Verifies SPEC-0001-R06.
curl ... /v1/servers | jq ...
```

`make spec-check` (part of `make qa` and CI) fails when:

- a spec has no valid status or its number does not match its directory;
- an `Accepted` or `Implemented` spec has no `plan.md`;
- a requirement ID is defined twice or belongs to a different spec;
- a requirement of an `Implemented` spec is not cited by any `*_test.go`
  file, chart unit test (`charts/**/tests/*_test.yaml`), `scripts/e2e-*.sh`,
  or file under `test/`;
- a test cites a requirement that no spec defines.

## Lifecycle

| Status | Meaning |
| --- | --- |
| `Draft` | Under discussion. Nothing is promised and nothing may be documented as available. |
| `Accepted` | Approved by a maintainer. `plan.md` exists. Implementation may start. |
| `Implemented` | Every requirement is built and cited by a test. User docs may describe it. |
| `Superseded` | Replaced by the spec named in the header. |
| `Withdrawn` | Abandoned. Kept for the record. |

The workflow:

1. Open an issue describing the problem.
2. Open a pull request that adds `spec.md` with status `Draft`. Review is about
   behavior only: requirements, acceptance criteria, non-goals, and security.
3. When a maintainer approves, set the status to `Accepted` and add `plan.md`
   (and `tasks.md` for work that spans several pull requests).
4. Implement in one or more pull requests. Each test cites the requirements it
   verifies.
5. The pull request that completes the work sets the status to `Implemented`
   and updates the README, component docs, and `ROADMAP.md`. `make spec-check`
   then proves every requirement has a test.

Changing an `Implemented` spec follows the same review: add new requirement
IDs for new behavior, mark replaced ones `Withdrawn`, and keep the status
`Implemented` only when the new requirements are tested in the same pull
request. Large redesigns get a new spec that supersedes the old one.

## Working with AI coding assistants

Specs are the input for assisted development. Point the assistant at the spec
and plan, ask it to work through `tasks.md`, and require each new test to cite
its requirement IDs. Review generated code against the requirements, not
against the assistant's summary of them.

## Index

| Spec | Title | Status |
| --- | --- | --- |
| [0001](0001-gateway-console/spec.md) | Gateway web console | Implemented |
| [0002](0002-console-v2/spec.md) | Console v2: operations UI | Accepted |
| [0003](0003-helm-chart-validation/spec.md) | Helm chart values validation and unit tests | Implemented |
| [0004](0004-reported-server-version/spec.md) | Reported MCP server version | Draft |
