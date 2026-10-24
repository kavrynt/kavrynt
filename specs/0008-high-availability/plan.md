# SPEC-0008 plan: High availability

## Design

- **Drain (R01).** `cmd/gateway/main.go` gains `--shutdown-delay`. On the
  signal, `main` calls `handler.SetDraining()`, which makes `/readyz` answer
  `503 {"status":"draining"}`; after the delay it calls
  `server.Shutdown(ctx)` with the existing timeout. The draining flag is an
  `atomic.Bool` in `api.Handler`. Endpoints controllers remove the pod
  within the delay, so no new request arrives after `Shutdown`. Draining
  also calls `server.SetKeepAlivesEnabled(false)`: responses carry
  `Connection: close` and idle connections close, so pooled clients open
  their next connection to a replica that stays. The sequence is the
  testable `shutdown` function in `cmd/gateway`.
- **Chart (R02–R05, R09).** Gateway Deployment: `strategy.rollingUpdate`
  with `maxUnavailable: 0`, `maxSurge: 1`; computed
  `terminationGracePeriodSeconds`; `topologySpreadConstraints` default
  (hostname and zone, `ScheduleAnyway`) when more than one replica.
  New `pdb.yaml` in both subcharts, `hpa.yaml` in the Gateway subchart
  (`autoscaling/v2`). `replicas` omitted when autoscaling is on. A `fail`
  in the Operator Deployment for R09.
- **Instance name (R06).** Downward API env `KAVRYNT_INSTANCE` from
  `metadata.name`; `--instance` flag defaulting to it, then hostname.
  `/v1/traffic` answers `{"instance": "...", "requests": [...]}`. The
  console renders it in the traffic page header (`app.js`, text only).
- **Operator (R08).** Leader election already exists (`--leader-elect`).
  Verify tool discovery and the Cloud connector run as leader-only
  runnables (controller-runtime runs runnables without
  `NeedLeaderElection() == false` on the leader only) and add tests.
- **Profile (R10, R11).** `examples/helm/values-production.yaml`.
  `scripts/e2e-kind.sh` installs with it (Kind single node: spread is
  `ScheduleAnyway`, so it schedules). The client is a pod running the e2e
  MCP server image in its `load` mode (`test/e2e/mcp-server/load.go`), so
  calls go through the Gateway Service inside the cluster, with pooled
  connections like a real client; it prints every failure. The test
  restarts the Gateway Deployment, waits for the rollout and the last
  drain, and fails on any `FAIL` line. It then deletes the Operator leader
  (the Lease holder) and applies a new `MCPServer`. The Operator releases
  the Lease on shutdown (`LeaderElectionReleaseOnCancel`).

## Affected files

| Path | Change |
| --- | --- |
| `cmd/gateway/main.go` | `--shutdown-delay`, `--instance`, drain sequence |
| `internal/gateway/api/api.go`, `traffic.go` | Draining readiness, instance in `/v1/traffic` |
| `internal/gateway/console/static/app.js` | Instance label on traffic page |
| `internal/operator/...` | Leader-only runnables check and tests |
| `charts/kavrynt/charts/gateway/templates/` | Deployment strategy, grace, spread, `pdb.yaml`, `hpa.yaml` |
| `charts/kavrynt/charts/k8s-operator/templates/` | `pdb.yaml`, R09 check |
| Both subcharts' `values.yaml`, `values.schema.json` | New values |
| `charts/kavrynt/tests/` | HA cases |
| `examples/helm/values-production.yaml` | New profile |
| `scripts/e2e-kind.sh`, `docs/E2E.md` | HA checks |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0008-R01 | `ha_test.go` draining readiness; `cmd/gateway/main_test.go` drain sequence; `e2e-kind.sh` rolling restart |
| SPEC-0008-R02, R03, R04, R05 | `ha_test.yaml` |
| SPEC-0008-R06 | `ha_test.go` instance field; `test/console/stats.test.mjs` scope note; `ha_test.yaml` env; `e2e-kind.sh` instance is a Gateway pod |
| SPEC-0008-R07 | `auth_test.go` two authenticators sharing a key directory |
| SPEC-0008-R08 | `cloudsync_test.go` leader-only connector; `e2e-kind.sh` leader deletion |
| SPEC-0008-R09 | `ha_test.yaml` |
| SPEC-0008-R10, R11 | `e2e-kind.sh` HA section |

## Rollout

Defaults unchanged (one replica each). Release notes: HA profile, new
values, `/v1/traffic` instance field, drain behavior on shutdown (pods take
up to 5 s longer to stop).

## Risks

- The rolling-restart check is timing-sensitive on CI. Mitigation: the
  client loop retries nothing and the test fails on any non-200, but the
  loop runs with a short client timeout and the delay is generous on CI.
- HPA requires metrics-server; without it the HPA reports unknown metrics
  and keeps `minReplicas`. Documented.
