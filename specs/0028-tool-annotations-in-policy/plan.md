# SPEC-0028 plan: Tool annotations in policy

## Decisions

- Absent hints stay absent in the policy input (open question 1, as
  recommended); the docs list MCP's defaults.
- `title` is not recorded (open question 2, as recommended).

## Design

- **API.** `MCPServerTool.Annotations *MCPServerToolAnnotations` with four
  `*bool` fields (`readOnlyHint`, `destructiveHint`, `idempotentHint`,
  `openWorldHint`), so absent and `false` differ. `Hints()` returns the set
  ones as a map. `CopyTools` copies them deeply. Both CRD copies gain the
  object with four `boolean` properties and no unknown fields, so the API
  server prunes anything else.
- **Discovery (R01, R02).** `buildResult` decodes `annotations` as a map of
  raw values and keeps a hint only when its exact key holds the JSON
  literal `true` or `false`. `statusSize` counts the encoded annotations,
  so they share `MaxStatusBytes` and the 200-tool bound.
- **Routes (R04, R05).** `kube.Routes` gives each route `Tools`, a map from
  tool name to its hints, built from `status.tools` only while
  `ToolsDiscovered` is `True`. A failed discovery keeps the last known tools
  in status (SPEC-0004-R02), but the policy treats them as not discovered.
  The map is never written after it is built.
- **Policy input (R03 to R07).** `policy.Input` gains `tool_annotations`
  (`map[string]bool`, never `null`) and `tool_discovered`. `policyInput`
  takes the route and sets `{}` and `false`. `toolInput(input, route, tool)`
  sets `tool`, `tool_annotations`, and `tool_discovered` from the route, and
  both `authorize` (`tools/call`) and the `tools/list` filter use it, so
  nothing comes from the request.
- **Console (R08).** `model.Tool` gains `annotations` (the hints map).
  `format.js` gets a DOM-free `toolHints(annotations)` that returns the
  labels in a fixed order (`read-only`, `destructive`, `idempotent`,
  `open world`, and `not …` for `false`); the tool views show them as
  badges with `textContent`.
- **Example (R09).** `examples/policy/read-only-tools.rego`: the read
  methods of `read-only.rego`, and `tools/call` only when
  `tool_discovered`, `tool_annotations.readOnlyHint == true`, and the route
  is in `trusted_servers`.

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `discovery.TestListToolsRecordsBehaviorHints` |
| R02 | `discovery.TestStatusSizeCountsAnnotations` |
| R03, R05, R07 | `api.TestPolicyInputHasTheRecordedToolHints` |
| R04 | `api.TestPolicyInputHasTheRecordedToolHints`, `kube.TestRoutesCarryDiscoveredToolHints` |
| R06 | `api.TestToolsListFilterUsesRecordedHints` |
| R08 | `test/console/format.test.mjs`, `kube.TestServersIncludeToolHints` |
| R09 | `policy.TestExamplePolicies` |

## Rollout and risks

- No new values. Servers gain hints at their next discovery.
- Hints are self-reported: the docs and the example say so, and the example
  trusts them only for allow-listed servers.
