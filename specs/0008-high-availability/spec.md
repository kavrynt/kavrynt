# SPEC-0008: High availability

| Field | Value |
| --- | --- |
| Status | Implemented |
| Owners | @Ravi041 |
| Created | 2026-10-24 |
| Issue | to be opened |
| ADRs | none |
| Supersedes | none |

## Summary

A tested high-availability profile: the Gateway runs several replicas that
survive node drains, rolling upgrades, and pod loss without failing MCP
calls, and the Operator runs a standby replica that takes over through
leader election. The chart gains PodDisruptionBudgets, topology spread,
optional autoscaling, and a production values example that the end-to-end
test installs.

## Problem

Both components run one replica by default, and nothing proves more than
one works. A Gateway restart drops in-flight MCP calls: the process stops
listening on `SIGTERM` while the Service still sends it traffic. There is no
PodDisruptionBudget, so a node drain can take every replica at once. Some
Gateway state is per replica (recent traffic, the console login limiter),
which is not stated anywhere.

## Goals

- Rolling restarts and upgrades of the Gateway complete without a failed
  MCP call.
- Losing the Operator leader recovers without manual action.
- Operators know which Gateway data is per replica.

## Non-goals

- Multi-cluster or multi-region failover.
- Sharing recent traffic or the login limiter across replicas (metrics in
  Prometheus are the cluster-wide view).
- Long-lived event streams surviving a Gateway restart: clients reconnect,
  as MCP Streamable HTTP expects.

## Requirements

### Gateway shutdown

- **SPEC-0008-R01** On `SIGTERM` the Gateway MUST immediately answer
  `/readyz` with `503`, keep serving all other requests for
  `--shutdown-delay` (default `5s`), then stop accepting connections and
  wait up to `--shutdown-timeout` for in-flight requests to finish.
- **SPEC-0008-R02** The chart MUST set `terminationGracePeriodSeconds` to at
  least the shutdown delay plus the shutdown timeout plus 5 seconds.
- **SPEC-0008-R03** The Gateway Deployment MUST use a rolling update with
  `maxUnavailable: 0`.

### Gateway replicas

- **SPEC-0008-R04** When `gateway.replicaCount` is greater than 1 or
  autoscaling is on, the chart MUST render a PodDisruptionBudget with
  `maxUnavailable: 1` (overridable), and default topology spread
  constraints across nodes and zones with `whenUnsatisfiable:
  ScheduleAnyway`, unless `topologySpreadConstraints` is set.
- **SPEC-0008-R05** With `gateway.autoscaling.enabled=true`, the chart MUST
  render a HorizontalPodAutoscaler on CPU utilisation with the configured
  minimum (at least 2) and maximum, and MUST NOT set `replicas` on the
  Deployment.
- **SPEC-0008-R06** `GET /v1/traffic` MUST include the answering replica's
  instance name (the pod name), and the console traffic page MUST show it,
  so readers know the list is per replica.
- **SPEC-0008-R07** Console sessions MUST be accepted by every replica
  (they are signed with the shared key; existing behavior to be tested).

### Operator replicas

- **SPEC-0008-R08** With `operator.replicaCount` greater than 1, exactly one
  replica MUST reconcile, discover tools, and sync to Kavrynt Cloud at a
  time; the others MUST stay ready and take over through leader election
  when the leader stops.
- **SPEC-0008-R09** The chart MUST refuse to render `operator.replicaCount`
  greater than 1 with `leaderElect: false`, and MUST render a
  PodDisruptionBudget for the Operator when it has more than one replica.

### Profile

- **SPEC-0008-R10** The repository MUST ship
  `examples/helm/values-production.yaml` (Gateway 3 replicas, Operator 2,
  PDBs, spread, resources) and the end-to-end test MUST install with it.
- **SPEC-0008-R11** With that profile, a rolling restart of the Gateway
  while a client calls `tools/list` continuously MUST produce no failed
  call, and deleting the Operator leader pod MUST let another replica
  reconcile a new `MCPServer` to `Ready`.

## Acceptance criteria

- Given 3 Gateway replicas, when `kubectl rollout restart` runs while a
  loop calls `tools/list` through the Service, then every call returns
  `200` (R01, R03, R11).
- Given `gateway.replicaCount=3`, then the chart renders a PDB with
  `maxUnavailable: 1` and spread constraints (R04).
- Given `operator.replicaCount=2`, when the leader pod is deleted and a new
  `MCPServer` is applied, then it becomes `Ready` (R08, R11).
- Given `operator.replicaCount=2` and `leaderElect=false`, then rendering
  fails (R09).
- Given two Gateway replicas, when the console logs in and reads
  `/v1/servers` repeatedly, then every request is authorized and
  `/v1/traffic` names an instance (R06, R07).

## Security and privacy

The console login limiter (SPEC-0005-R09) counts failures per replica, so
an attacker spread across N replicas gets N times the attempts; with a
32-character generated password this does not change the risk, and the
docs state it. The instance name exposed in `/v1/traffic` is a pod name,
already visible to anyone with cluster read access; it is behind the
console login.

## Compatibility

New values: `gateway.autoscaling`, `gateway.podDisruptionBudget`,
`gateway.topologySpreadConstraints`, `operator.podDisruptionBudget`,
`gateway.config.shutdownDelay`. New Gateway flag `--shutdown-delay`.
`/v1/traffic` gains a top-level `instance` field (additive). Defaults keep
one replica of each component, so upgrades change nothing until values are
set. No CRD change.

## Documentation

Chart README (HA section, per-replica data), Gateway and Operator component
docs, README "Gateway is single-replica by default" line, ROADMAP item 7.

## Decisions

- Defaults stay at one replica: small trial installs keep their footprint;
  production installs use the profile.
- Drain in the Gateway (R01) rather than a `preStop` hook: the image is
  distroless (no `sleep`), and native `preStop` sleep needs Kubernetes 1.30+.
