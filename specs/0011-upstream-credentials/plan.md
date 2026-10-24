# SPEC-0011 plan: Upstream credentials

## Design

- **API (R01).** `api/v1alpha1`: `MCPServerCredential{Header, Scheme}` and
  `MCPServerSpec.Credential *MCPServerCredential`. Validation in
  `validation.go` (shared by the Operator and `kavryctl`); the hand-written
  CRD gains the schema with a `pattern` for `header` and a CEL rule
  (`x-kubernetes-validations`) for the deny list and the `stdio` case.
  Both CRD copies change together.
- **Shared reader (R02–R04).** New `internal/upstreamcred` package:
  `Store{Dir string}` with `Lookup(namespace, name, endpoint string)
  (value string, ok bool)`. It reads `<dir>/<ns>.<name>` and
  `<dir>/<ns>.<name>.origin` on each call (files are small; the kubelet
  updates them atomically through symlinks), normalises the endpoint's
  origin with `net/url` (lower-case host, explicit default port), and
  compares. No caching, so rotation is immediate (R04). Errors never
  include values (R09).
- **Gateway (R05–R07).** `api.Handler.EnableUpstreamCredentials(store)`.
  In the proxy path, after credential headers are stripped and before the
  request is sent: if the route's model carries a credential spec, look it
  up; on miss write `503` and record `credential_unavailable`. The route
  model (`model.Route` internal fields, not JSON) carries the credential
  spec from the watcher. `/v1/servers` gains `credential:
  {required, available}`.
- **Operator (R08).** The discovery client takes the same `Store` and sets
  the header; a miss sets `ToolsDiscovered=False/CredentialUnavailable`.
- **Chart (R02).** When `global.upstreamCredentials.secretName` is set, both
  Deployments mount it read-only at `/etc/kavrynt/upstream-credentials`
  (mode `0444`, as with the console Secret, and `optional: true` so pods
  start before the Secret exists) and pass `--upstream-credentials-dir`.
  RBAC templates are untouched.
- **End to end (R10).** `test/e2e/mcp-server` gains `REQUIRE_BEARER`
  (env). `e2e-kind.sh` creates the Secret before installing (so no wait for
  the kubelet to refresh a mount), deploys a server requiring the token and
  two `MCPServer`s: one with a stored, pinned credential (discovered and
  routed) and one without (`503`, `CredentialUnavailable`). It checks Secret
  RBAC for both components and scans responses, status, metrics, and logs
  for the value. Rotation and pin mismatch are unit-tested.

## Affected files

| Path | Change |
| --- | --- |
| `api/v1alpha1/mcpserver_types.go`, `validation.go`, tests | `credential` field |
| `config/crd/bases/...`, chart CRD copy | Schema |
| `internal/upstreamcred/` | New |
| `internal/gateway/api/`, `kube/`, `model/` | Credential injection, `/v1/servers` |
| `internal/operator/discovery/`, `controller/` | Discovery credential, condition reason |
| `cmd/gateway`, `cmd/operator` | `--upstream-credentials-dir` |
| Chart values, schemas, Deployments, tests | Mount |
| `test/e2e/mcp-server/main.go`, `scripts/e2e-kind.sh` | Credentialed server |
| `examples/kavryctl/` | Example |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0011-R01 | `validation_test.go`; `kavryctl` manifest tests; `e2e-kind.sh` CRD rejection |
| SPEC-0011-R02 | Chart tests (mount, no RBAC change); `e2e-kind.sh` RBAC check |
| SPEC-0011-R03, R04 | `upstreamcred` tests (pin match, mismatch, default ports, rotation) |
| SPEC-0011-R05, R06, R07 | `api_test.go` with fake upstream |
| SPEC-0011-R08 | Discovery and controller tests |
| SPEC-0011-R09 | Log capture tests in `api` and `discovery`; status and traffic assertions |
| SPEC-0011-R10 | `e2e-kind.sh` credential section |

## Rollout

Inert until a server declares `credential`. Release notes: new field,
Secret layout, pin requirement, new outcome and reason.

## Risks

- Administrators forget the `.origin` key and see `503`. Mitigation: the
  console shows "credential required, not available", and the message in
  the Operator condition names the missing key (not the value).
