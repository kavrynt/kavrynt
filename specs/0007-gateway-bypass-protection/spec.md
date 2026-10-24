# SPEC-0007: Gateway bypass protection

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) ("Gateway bypass") |
| Supersedes | none |

## Summary

The chart can install Kubernetes NetworkPolicies so that MCP server pods
accept traffic only from the Kavrynt Gateway (and the Operator, for tool
discovery), and so that the Gateway accepts traffic only from the clients a
platform team allows. Authentication, policy, and audit in the Gateway are
only as strong as this: an agent that can reach an MCP server directly
skips all of them.

## Problem

Every MCP server registered with Kavrynt is an ordinary Kubernetes Service.
Any pod in the cluster that can resolve its name can call its tools
directly, without passing the Gateway. ADR-0003 calls for an optional
NetworkPolicy to close that path; the chart does not ship one.

## Goals

- A platform team can mark MCP server pods so that only the Gateway and the
  Operator can reach them, with one Helm value per namespace.
- A platform team can restrict which namespaces may call the Gateway.
- Off by default, so existing installs and clusters without a
  NetworkPolicy-capable CNI keep working.

## Non-goals

- Egress restrictions for the Gateway or the Operator: MCP endpoints can be
  anywhere, and `internal/egress` already refuses metadata, link-local, and
  loopback addresses.
- Detecting whether the cluster's CNI enforces NetworkPolicy. The docs state
  the requirement.
- Policies for MCP servers outside Kubernetes (external endpoints).
- Labelling MCP server pods automatically: the Operator does not own those
  workloads.

## Requirements

### MCP server isolation

- **SPEC-0007-R01** With `networkPolicy.mcpServers.enabled=true`, the chart
  MUST render one NetworkPolicy in each namespace listed in
  `networkPolicy.mcpServers.namespaces`, selecting pods labelled
  `kavrynt.io/gateway-only: "true"`.
- **SPEC-0007-R02** That NetworkPolicy MUST allow ingress to the selected
  pods only from the Gateway pods and the Operator pods of this release
  (namespace and pod label selectors), on any port.
- **SPEC-0007-R03** The chart MUST refuse to render when
  `networkPolicy.mcpServers.enabled=true` and the namespace list is empty,
  with a message naming the value.
- **SPEC-0007-R04** When `global.watchNamespaces` is set, every namespace in
  `networkPolicy.mcpServers.namespaces` MUST be one of them; otherwise the
  chart MUST refuse to render.
- **SPEC-0007-R05** Pods without the label MUST NOT be selected: the
  policies change nothing for workloads that did not opt in.

### Gateway ingress

- **SPEC-0007-R06** With `networkPolicy.gateway.enabled=true`, the chart
  MUST render a NetworkPolicy for the Gateway pods that allows ingress on
  the Gateway port only from namespaces matching
  `networkPolicy.gateway.allowedNamespaceLabels` and from CIDRs in
  `networkPolicy.gateway.allowedCIDRs`, and from the Gateway's own
  namespace.
- **SPEC-0007-R07** The Gateway NetworkPolicy MUST NOT restrict egress.

### Defaults and validation

- **SPEC-0007-R08** Both policies MUST be off by default, and the values
  schema MUST reject unknown keys and malformed CIDRs.

### End to end

- **SPEC-0007-R09** In a cluster that enforces NetworkPolicy, with isolation
  enabled for a namespace, a call through the Gateway to a labelled MCP
  server MUST succeed, tool discovery MUST still work, and a direct call
  from another pod to the same server MUST fail.

## Acceptance criteria

- Given `networkPolicy.mcpServers.enabled=true` and namespace `team-a`, when
  the chart is rendered, then `team-a` gets a NetworkPolicy selecting
  `kavrynt.io/gateway-only: "true"` pods with ingress from the Gateway and
  Operator only (R01, R02).
- Given that policy in a Kind cluster, when a test pod in `default` calls
  the MCP server Service directly, then the connection fails; the same call
  through `/mcp/team-a.<name>` succeeds, and `status.tools` is populated
  (R09).
- Given isolation enabled with no namespaces, when the chart is rendered,
  then it fails naming `networkPolicy.mcpServers.namespaces` (R03).
- Given `networkPolicy.gateway.allowedCIDRs: ["10.0.0.0/8"]`, then the
  Gateway policy admits that CIDR and the release namespace only, and has no
  egress rules (R06, R07).

## Security and privacy

This adds no access; it removes network paths. A mislabelled pod is not
protected (fail open by design, because the chart cannot know which pods are
MCP servers); the docs say so. NetworkPolicies are enforced only by a CNI that
supports them; on other clusters they are inert. Policies live in customer
namespaces, so the chart's RBAC needs no change: Helm creates them with the
installer's permissions.

## Compatibility

New Helm values under `networkPolicy` (top level of the umbrella chart), off
by default. No CRD change, no Gateway or Operator code change. Upgrades from
`0.0.5-beta.1` are unaffected until enabled.

## Documentation

Chart README (values, label, CNI requirement), Gateway component doc
(security section), README security posture.

## Decisions

- Opt-in label rather than selecting every pod in a namespace: a namespace
  may host other workloads the Gateway should not reach.
- The Operator keeps direct access for tool discovery (SPEC-0004 and the
  discovery feature depend on it).
