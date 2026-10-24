# KavryntOperatorDown

## Meaning

Prometheus has scraped no healthy Operator target for 5 minutes.

## Impact

Critical. New and changed `MCPServer` resources are not validated, so they do
not become `Ready` and the Gateway does not route them. Existing routes keep
working. Tool discovery and the Kavrynt Cloud connector stop.

## Diagnosis

```bash
kubectl -n kavrynt-system get deployment,pods -l app.kubernetes.io/name=operator -o wide
kubectl -n kavrynt-system logs -l app.kubernetes.io/name=operator --tail=100
kubectl -n kavrynt-system get lease kavrynt-k8s-operator -o jsonpath='{.spec.holderIdentity}{"\n"}'
```

- Pods not running: see their events.
- Pods healthy: Prometheus cannot scrape the `kavrynt-operator-metrics`
  Service. Check `operator.metrics.serviceMonitor.enabled` and
  `metrics.prometheusRule.jobs.operator`.

## Mitigation

Fix the cause above; restart with `kubectl -n kavrynt-system rollout restart
deployment/kavrynt-operator`. Run two replicas so a standby takes over through
leader election.
