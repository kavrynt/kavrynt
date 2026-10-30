# SPEC-0012: Tool authorization policy

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("Policy decision point inside the gateway") |
| Supersedes | none |

## Summary

The Gateway evaluates a policy before forwarding every MCP request: who is
calling (from SPEC-0006), which server, which MCP method, and which tool.
Policies are written in Rego, loaded from a ConfigMap, and evaluated by Open
Policy Agent embedded in the Gateway. Anything the policy does not allow is
denied. Platform teams can say, for example, "only the `support-agents`
client may call `crm.update_ticket`" or "no one may call tools on
`prod-db` outside business hours".

## Problem

With client authentication (SPEC-0006), the Gateway knows who is calling,
but every authenticated caller can still call every tool of every routed
server. There is no way to restrict a tool to some callers, keep a
destructive tool off limits, or allow read-only methods only.

## Goals

- Every MCP request through the Gateway is allowed or denied by policy,
  with a reason.
- A policy mistake fails closed, and a bad policy update never replaces a
  working one.
- Policy authors can test policies against documented input.

## Non-goals

- `require_approval` decisions and approval workflows (Kavrynt Cloud).
- Policy bundles delivered and signed by Kavrynt Cloud (own spec); this
  spec loads policy from a ConfigMap, protected by Kubernetes RBAC.
- Filtering `tools/list` results to the tools a caller may use (follow-up;
  a denied `tools/call` is already enforced).
- Inspecting or rewriting tool arguments or results.
- Policy for the console or `/v1/*` endpoints.

## Requirements

### Enabling and loading

- **SPEC-0012-R01** Policy MUST be off unless `gateway.policy.enabled=true`
  (flag `--policy-dir`); when off, `/mcp/` behaves as before.
- **SPEC-0012-R02** When on, the Gateway MUST load every `.rego` file from
  the ConfigMap named by `gateway.policy.configMap`, mounted by the kubelet
  (no ConfigMap RBAC), and evaluate `data.kavrynt.authz.decision`.
- **SPEC-0012-R03** If the policy does not compile at start-up, the Gateway
  MUST exit with an error naming the file and line (not start serving).
- **SPEC-0012-R04** When the mounted policy changes, the Gateway MUST
  compile it and switch to it atomically; if it does not compile, the
  Gateway MUST keep the previous policy, log the error, and count it in
  `kavrynt_gateway_policy_load_failures_total`.
- **SPEC-0012-R05** The Gateway MUST export
  `kavrynt_gateway_policy_info{revision}` with the SHA-256 of the active
  policy files, so every replica's active revision is visible.

### Input and decision

- **SPEC-0012-R06** The policy input MUST contain: `subject`, `client_id`,
  `issuer`, `scopes` (empty when SPEC-0006 authentication is off);
  `route.namespace`, `route.name`; `http_method`; `mcp_method` (JSON-RPC
  method, empty for non-POST requests); `tool` (for `tools/call`, else
  empty); and `time` (RFC 3339, UTC). Its JSON schema MUST be documented.
- **SPEC-0012-R07** A request MUST be forwarded only when the decision is
  an object with `allow` equal to `true`. An undefined decision, a
  non-object, an evaluation error, or an evaluation longer than 100 ms MUST
  deny.
- **SPEC-0012-R08** A denied request MUST get `403` with a JSON-RPC error
  body carrying the request `id` (when known) and the message
  `denied by policy` followed by the policy's `reason` string (at most 200
  characters, control characters removed), MUST NOT reach the MCP server,
  and MUST be recorded with outcome `denied`. Superseded by SPEC-0036 for
  JSON-RPC requests, which get `200` with that error.

### Classification

- **SPEC-0012-R09** When on, a `POST` body that cannot be classified
  (larger than the inspection limit, not JSON, a JSON-RPC batch, a
  `tools/call` without a string `params.name`, or an object in which
  `jsonrpc`, `id`, `method`, `params`, or `params.name` appears twice or
  under a name that matches it only case-insensitively) MUST be denied
  with `400` and not forwarded.
- **SPEC-0012-R10** When the request carries `Mcp-Method` or `Mcp-Name`
  headers that disagree with the body, it MUST be refused with `400` and
  not forwarded.

### Examples

- **SPEC-0012-R11** The repository MUST ship example policies (allow all
  for authenticated callers; per-client tool allow-list; read-only methods
  only), and each MUST be evaluated by a Go test against allowed and denied
  inputs.

### End to end

- **SPEC-0012-R12** With authentication and the per-client allow-list
  example enabled, an allowed client's `tools/call` MUST succeed and
  another client's MUST get `403`; replacing the ConfigMap with an invalid
  policy MUST leave the previous decisions in force. Superseded by SPEC-0036
  for the status: the other client's `tools/call` gets `200` with the
  JSON-RPC error.

## Acceptance criteria

- Given the allow-list policy permitting client `support-agent` to call
  `search` on `team-a.search`, when that client calls it, then the call is
  proxied; when client `etl` calls it, then the JSON-RPC error `denied by
  policy: tool not allowed for client` (with `200` since SPEC-0036, `403`
  before) and outcome `denied` (R07, R08, R12).
- Given a policy file with a syntax error at start-up, then the Gateway
  exits with the file and line (R03).
- Given a valid policy in force, when the ConfigMap is updated with a
  syntax error, then decisions do not change and
  `kavrynt_gateway_policy_load_failures_total` increases (R04, R12).
- Given a policy whose `decision` is undefined for an input, then the
  request is denied (R07).
- Given a `tools/call` with `Mcp-Name: search` and body `params.name:
  delete_all`, then `400` (R10).

## Security and privacy

Default deny and fail closed on every error path (R03, R07, R09). Policy
input contains identity claims and the tool name, never tool arguments.
Policy files are trusted configuration: whoever can write the ConfigMap
controls authorization, so the docs tell operators to restrict it with
RBAC. The `reason` returned to clients is policy-author text, length
limited and sanitised. Evaluation is time-bounded so a pathological policy
cannot stall the Gateway. Embedding OPA adds a significant dependency; it
must pass govulncheck and Trivy, and only the `rego` evaluation package is
used (no OPA server, no bundle downloads, no network access from policy:
`http.send` and other network built-ins are disabled).

## Compatibility

Opt-in. New values `gateway.policy.enabled`, `gateway.policy.configMap`;
new flag `--policy-dir`. New outcome `denied`. New Go dependency
`github.com/open-policy-agent/opa` (pinned). No CRD change. Gateway image
grows by the OPA library size.

## Documentation

Gateway component doc (policy section, input schema, examples), chart
README, `examples/policy/`, README security posture, CLAUDE.md "What works
today".

## Decisions

- OPA embedded, as ADR-0003 decided. CEL (lighter, Kubernetes-native) was
  considered; changing it would need an ADR-0003 revision.
- Policy works with or without SPEC-0006 authentication (identity fields
  empty), so tool restrictions can be applied before authentication is
  rolled out; the docs recommend both.
- Invalid policy at start-up stops the Gateway (loud, and a rolling update
  keeps the old pods serving); invalid policy on reload keeps the last good
  one.
