# SPEC-0004 plan: Reported MCP server version

## Design

- **API.** `MCPServerStatus.ServerInfo *MCPServerInfo` with `name` and
  `version`, both optional. Both CRD copies get `status.serverInfo` with
  `maxLength: 128` on each field, and a `Reported` printer column on
  `.status.serverInfo.version` (R05).
- **Discovery.** `ListTools` already decodes the `initialize` result; it
  also decodes `serverInfo` and returns it in `Result.ServerInfo`, for
  servers with and without the tools capability. A value is kept only when
  it is valid UTF-8, non-blank, at most 128 characters, and has no control
  characters; otherwise that field is empty, and a `ServerInfo` with both
  fields empty is nil (R01).
- **Controller.** `discoveryAttempt` carries `serverInfo` next to `tools`
  and follows the same rules: replaced on success, kept on failure for the
  same endpoint (or taken from status after an Operator restart), dropped
  when the endpoint changes, cleared when the server is not `Ready` or
  discovery is disabled (R02). Conditions are untouched.
- **Gateway.** `model.Server` gets `ServerInfo` (omitted when nil); the
  watcher copies it from status; `/v1/servers` serves it (R03).
- **Console.** `stats.js` `versionInfo(server)` returns the declared and
  reported versions and whether they differ. Cards and the server detail
  show "declared" and "reported" and a "version differs" badge; diagnostics
  include the reported version. A mismatch is shown, not acted on: it does
  not change server health (R04).
- **kavryctl.** `inspect` prints the object, so `status.serverInfo` appears
  through the Go type (R05).

The Cloud connector does not send `serverInfo` (open question in the spec).

## Affected files

| Path | Change |
| --- | --- |
| `api/v1alpha1/mcpserver_types.go`, deepcopy | `MCPServerInfo` |
| `config/crd/bases/…`, `charts/…/crds/…` | `status.serverInfo`, `Reported` column |
| `internal/operator/discovery/` | Decode and bound `serverInfo` |
| `internal/operator/controller/` | Record, keep, and clear it |
| `internal/gateway/model`, `internal/gateway/kube/watcher.go` | Carry it to `/v1/servers` |
| `internal/gateway/console/static/stats.js`, `app.js` | Show it and the mismatch |
| `internal/kavryctl/cli` tests | `inspect` output |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0004-R01 | `discovery_test.go` valid, missing, oversized, and control-character `serverInfo` |
| SPEC-0004-R02 | `mcpserver_controller_test.go` success, failure, endpoint change, not Ready |
| SPEC-0004-R03 | `servers_test.go` `/v1/servers` JSON with and without `serverInfo` |
| SPEC-0004-R04 | `stats.test.mjs` `versionInfo` and health; `smoke.mjs` card and detail |
| SPEC-0004-R05 | `cli_test.go` inspect output; `e2e-kind.sh` `REPORTED` column |

## Rollout

Additive. Existing servers get `status.serverInfo` on their next discovery
(within the tool refresh interval). No flag.

## Risks

- `serverInfo` is self-reported and untrusted; it is bounded, rendered as
  text, and never used for decisions.
