# SPEC-0007 plan: Gateway bypass protection

## Design

- **Where the templates live.** Both NetworkPolicies need the Gateway's and
  the Operator's pod labels, so they are rendered by the umbrella chart
  (`charts/kavrynt/templates/networkpolicy.yaml`), which knows both
  subcharts' `fullnameOverride` and the release name. Pod selectors use
  `app.kubernetes.io/name` and `app.kubernetes.io/instance`, the labels the
  subcharts already set.
- **MCP server policy.** One `NetworkPolicy` per listed namespace, named
  `<release>-mcp-gateway-only`, `podSelector` on
  `kavrynt.io/gateway-only: "true"`, `policyTypes: [Ingress]`, one ingress
  rule with two peers: `namespaceSelector` on
  `kubernetes.io/metadata.name: <release namespace>` combined with the
  Gateway pod selector, and the same for the Operator.
- **Gateway policy.** `podSelector` on the Gateway labels,
  `policyTypes: [Ingress]` only (R07), peers from
  `allowedNamespaceLabels` (one `namespaceSelector` per entry),
  `allowedCIDRs` (`ipBlock`), and the release namespace, port = Gateway
  service port.
- **Validation.** `fail` in the template for R03 and R04; the umbrella
  `values.schema.json` gains `networkPolicy` with
  `additionalProperties: false` and a CIDR `pattern` (R08).
- **End to end.** Kind's default CNI (kindnet) enforces NetworkPolicy.
  `scripts/e2e-kind.sh` labels the test MCP server pod, upgrades the release
  with isolation for its namespace, then checks the Gateway path, tool
  discovery, and a direct `curl` from a throwaway pod (expect failure).

## Affected files

| Path | Change |
| --- | --- |
| `charts/kavrynt/templates/networkpolicy.yaml` | New |
| `charts/kavrynt/values.yaml`, `values.schema.json` | `networkPolicy` values |
| `charts/kavrynt/tests/networkpolicy_test.yaml` | New chart unit tests |
| `scripts/e2e-kind.sh` | Isolation check |
| `examples/` | Labelled MCP server example |
| Chart README, `docs/components/gateway/README.md`, `README.md` | On `Implemented` |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0007-R01, R02, R05 | `networkpolicy_test.yaml` rendered selectors and peers |
| SPEC-0007-R03, R04 | `networkpolicy_test.yaml` `failedTemplate` cases |
| SPEC-0007-R06, R07 | `networkpolicy_test.yaml` Gateway policy |
| SPEC-0007-R08 | `values_schema_test.yaml` defaults and rejected values |
| SPEC-0007-R09 | `scripts/e2e-kind.sh` isolation section |

## Rollout

Off by default. Enable per namespace; label the MCP server pods first, then
enable, so nothing is cut off during the change. Release notes: new opt-in
isolation, CNI requirement, label name.

## Risks

- A CNI that ignores NetworkPolicy gives a false sense of safety.
  Mitigation: docs, and the e2e proves enforcement only on Kind.
- Isolating a namespace whose MCP servers are also called by other
  workloads cuts those callers off. Mitigation: opt-in label per pod.
