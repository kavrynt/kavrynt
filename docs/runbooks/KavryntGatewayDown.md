# KavryntGatewayDown

## Meaning

Prometheus has scraped no healthy Gateway target for 5 minutes: every
replica is down, not ready to be scraped, or unreachable from Prometheus.

## Impact

Critical. MCP clients cannot reach any routed server through
`/mcp/<namespace>.<name>`, and the console is unavailable.

## Diagnosis

```bash
kubectl -n kavrynt-system get deployment,pods -l app.kubernetes.io/name=gateway -o wide
kubectl -n kavrynt-system describe pods -l app.kubernetes.io/name=gateway
kubectl -n kavrynt-system logs -l app.kubernetes.io/name=gateway --tail=100
kubectl -n kavrynt-system get events --sort-by=.lastTimestamp | tail -20
```

- Pods `Pending`: look for scheduling or quota events.
- `CrashLoopBackOff`: the log names the cause, for example
  `configure console login` when the console admin Secret is missing.
- Pods `Running` but not ready: `/readyz` answers `503` until the
  `MCPServer` cache has synced once; see
  [KavryntGatewayRouteSyncFailing](KavryntGatewayRouteSyncFailing.md).
- Pods healthy: Prometheus cannot scrape them. Check the ServiceMonitor
  (`gateway.metrics.serviceMonitor.enabled`), a NetworkPolicy on the Gateway
  (`networkPolicy.gateway` must admit Prometheus' namespace), and that
  `metrics.prometheusRule.jobs.gateway` matches the scrape job.

## Mitigation

Fix the cause shown above, then `kubectl -n kavrynt-system rollout restart
deployment/kavrynt-gateway` if pods do not recover. Run several replicas
(`examples/helm/values-production.yaml`) so one failure does not take the
Gateway down.
