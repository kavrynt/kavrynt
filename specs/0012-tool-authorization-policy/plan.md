# SPEC-0012 plan: Tool authorization policy

## Design

- **Package `internal/gateway/policy`.** `Engine` holds an
  `atomic.Pointer[compiled]` (prepared `rego.PreparedEvalQuery` plus
  revision). `Load(dir)` reads `*.rego` in sorted order, compiles with
  `rego.New(rego.Query("data.kavrynt.authz.decision"), rego.Module(...),
  rego.Capabilities(<default minus network built-ins>), rego.StrictBuiltinErrors(true))`,
  and returns file/line errors (R02, R03). `Watch(ctx)` polls the
  directory's `..data` symlink target every 10 s (kubelet atomic update)
  and reloads on change (R04). `Decide(ctx, Input) Decision` evaluates with
  a 100 ms context deadline and maps anything but
  `{"allow": true}` to deny (R07).
- **Input (R06).** `policy.Input` struct with JSON tags; the identity part
  comes from the SPEC-0006 authenticator through the request context;
  `mcp_method` and `tool` from the existing request classification
  (`inspectRequest`), extended to report "unclassifiable" (R09) and header
  disagreement (R10).
- **Handler.** `api.Handler.EnablePolicy(engine)`. In the `/mcp/` path:
  authenticate (SPEC-0006, when on) → classify → policy → strip and
  forward. Denials write the JSON-RPC error (R08) and record `denied`.
- **Metrics (R04, R05).** `kavrynt_gateway_policy_load_failures_total`,
  `kavrynt_gateway_policy_info{revision}` (gauge set to 1 for the active
  revision, previous one deleted), decision counter by result.
- **Chart.** Values, schema, ConfigMap volume mounted at
  `/etc/kavrynt/policy`, `--policy-dir`. Fail render when enabled without
  `configMap`.
- **Examples (R11).** `examples/policy/{allow-authenticated,client-allowlist,read-only}.rego`;
  `internal/gateway/policy/examples_test.go` loads them from the repo.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/policy/` | New engine, watch, tests |
| `internal/gateway/api/api.go`, `traffic.go` | Policy step, classification, outcome |
| `cmd/gateway/main.go` | `--policy-dir` |
| `go.mod`, `go.sum` | OPA dependency |
| Gateway chart values, schema, Deployment, tests | Policy mount |
| `examples/policy/` | Examples |
| `scripts/e2e-kind.sh` | Policy section |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0012-R01 | `api_test.go` policy off |
| SPEC-0012-R02, R03, R04, R05 | `policy` engine tests (load, bad file, reload, keep last good, metrics) |
| SPEC-0012-R06, R07 | `policy` decision tests (undefined, error, timeout, non-object) |
| SPEC-0012-R08, R09, R10 | `api_test.go` denial body, unclassifiable, header mismatch |
| SPEC-0012-R11 | `examples_test.go` |
| SPEC-0012-R12 | `e2e-kind.sh` policy section |

## Rollout

Off by default. Release notes: policy engine, input schema, examples, fail
closed behavior, request size limit when on.

## Risks

- OPA increases binary size and dependency surface. Mitigation: govulncheck
  and Trivy gates; only `rego` imported.
- Requests above the inspection limit (1 MiB) are denied when policy is on.
  Documented; raised later if real tools need it.
