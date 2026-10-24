# KavryntCloudSyncStale

## Meaning

Kavrynt Cloud has not accepted an inventory snapshot from the Operator for
longer than `metrics.prometheusRule.cloudSyncStaleSeconds` (default 600).
Only rendered when `operator.cloud.enabled` is true.

## Impact

Warning. The cluster's servers and tools in Kavrynt Cloud are out of date.
Routing in the cluster is not affected.

## Diagnosis

```bash
kubectl -n kavrynt-system logs -l app.kubernetes.io/name=operator --tail=200 | grep -i cloud
```

`kavrynt_operator_cloud_sync_failure_total{reason}` counts failures by reason.
Common causes: the cluster token Secret (`operator.cloud.tokenSecret`) is
missing, empty, or revoked (`401`); `operator.cloud.url` is wrong; egress to
Kavrynt Cloud is blocked by a proxy or firewall.

## Mitigation

Fix the token Secret (the Operator re-reads it before every sync, no restart
needed) or the network path. Syncs resume on the next interval
(`operator.cloud.syncInterval`).
