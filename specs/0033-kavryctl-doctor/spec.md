# SPEC-0033: kavryctl doctor

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-30 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

`kavryctl doctor` checks a Kavrynt installation from the current
Kubernetes context and reports, in one screen, what works, what is wrong,
and how to fix it. Its report holds no secrets, and with `--anonymize` no
names, so testers can paste it into a public issue.

## Problem

Developers testing the beta ([docs/TESTING.md](../../docs/TESTING.md)) who
hit a problem have to collect `kubectl get`, `kubectl describe`, and logs
from several resources, and know which ones matter. Their feedback issues
then miss the facts maintainers need (versions, conditions, settings), or
include more than they should (endpoints, tokens in logs). Most first-run
problems are a short list of known causes: the CRD missing or out of date,
a Deployment not ready, an install with client authentication off, an
`MCPServer` not `Ready` or not discovered.

## Goals

- One read-only command that finds the common installation and
  registration problems and names the fix for each.
- A report safe to share publicly: no secrets ever, no names on request.
- A stable machine-readable form, for CI and for the feedback issue
  template.

## Non-goals

- Fixing anything. `doctor` never changes the cluster.
- Collecting logs or a full support bundle.
- Probing MCP servers or calling `/mcp/` routes; the Operator's conditions
  already report reachability and discovery (SPEC-0027).
- Checks of Kavrynt Cloud.

## Requirements

### Running

- **SPEC-0033-R01** `kavryctl doctor` MUST use the current kubeconfig
  context (or `--kubeconfig` and `--context`), find the release in
  `--namespace` (default `kavrynt-system`), and make only `get` and `list`
  requests. It MUST NOT read Secrets.
- **SPEC-0033-R02** A check whose resource `doctor` may not read MUST be
  reported as `skipped`, naming the missing verb and resource, and the other
  checks MUST still run.
- **SPEC-0033-R03** The exit code MUST be `0` when no check failed, `1`
  when one failed, and `2` when the cluster could not be reached at all.
  Warnings MUST NOT change the exit code.

### Checks

- **SPEC-0033-R04** `doctor` MUST check, each with the result `ok`, `warn`,
  `fail`, or `skipped`:
  1. the `MCPServer` CRD is installed and serves `v1alpha1`;
  2. the Operator and Gateway Deployments have their desired replicas ready;
  3. the Operator, Gateway, and `kavryctl` versions are the same (`warn`
     when `kavryctl` differs, `fail` when the Operator and Gateway differ);
  4. MCP client authentication is on (`warn` when the Gateway runs with it
     off);
  5. every `MCPServer` is `Ready` (`fail` per server that is not, with the
     reason of its condition);
  6. every `Ready` server's tool discovery succeeded (`warn` per server whose
     `ToolsDiscovered` is not `True`, with its reason).
- **SPEC-0033-R05** Every `warn` and `fail` MUST carry one line saying what
  to do, and a link to the runbook or doc section that explains it.

### Output

- **SPEC-0033-R06** The default output MUST be a table of checks with their
  result and fix, followed by a summary line. `--output json` MUST print
  one JSON object with `"type": "kavrynt.doctor.v1"`, the versions, and the
  checks (`id`, `result`, `subject`, `reason`, `fix`, `link`), with stable
  check IDs.
- **SPEC-0033-R07** The report MUST NOT contain Secret data, tokens,
  passwords, endpoint URLs, or headers. Condition messages MAY be included:
  the Operator writes them without endpoints or addresses (SPEC-0027).
- **SPEC-0033-R08** With `--anonymize`, namespaces and `MCPServer` names in
  the report MUST be replaced by stable short hashes (the same name gives
  the same hash within one report), and condition messages MUST be left
  out.

## Acceptance criteria

- Given a healthy installation, when a developer runs `kavryctl doctor`,
  then every check is `ok` and the exit code is `0` (R03, R04).
- Given an `MCPServer` whose endpoint reaches another namespace, then its
  check is `fail` with reason `EndpointNotAllowed` and a link to the doc
  that explains it, and the exit code is `1` (R04, R05).
- Given an `MCPServer` whose endpoint does not answer, then it is `Ready`,
  its discovery check is `warn` with reason `DiscoveryFailed` and a link to
  the runbook, and the exit code is `0` (R03, R04, R05).
- Given an install with `gateway.auth.enabled=false`, then the
  authentication check is `warn` and the exit code is `0` (R03, R04).
- Given a user who may list `MCPServer`s but not Deployments, then the
  Deployment checks are `skipped` naming `list deployments` and the
  `MCPServer` checks still run (R02).
- Given `--output json --anonymize`, then the output parses as
  `kavrynt.doctor.v1` and contains no namespace or server name and no
  condition message (R06, R08).
- Given any installation, then no output contains a Secret value, a token,
  or an endpoint URL (R07).

## Security and privacy

`doctor` reads cluster state with the user's own credentials and needs no
new RBAC. It never reads Secrets, so it cannot leak them. Endpoints are left
out because they can name internal hosts. Condition messages come from the
Operator, which never quotes servers or addresses (SPEC-0027); `--anonymize`
drops them anyway for public reports.

## Compatibility

A new `kavryctl` command; nothing else changes. It works against the
current and previous release. Older Gateways and Operators are recognized
by their image tags.

## Documentation

`docs/components/kavryctl/README.md`, `docs/TESTING.md` (run it when
something fails), the tester feedback issue template (paste
`kavryctl doctor --anonymize`), `ROADMAP.md`.

## Open questions

- Should `doctor` also check the chart's optional features (policy ConfigMap
  present, audit on, NetworkPolicies)? Recommended: report them as `info`
  lines in a later version; the first version checks what breaks a trial.
- Should check 3 read versions from the Helm release instead of image tags?
  Recommended: image tags, so `doctor` does not need the `helm` binary or
  read the Helm release Secret.
