---
name: e2e-debug
description: Run the Kavrynt Kind end-to-end workflow (make e2e-kind) and diagnose failures - which of the eight contract checks failed, why, and the smallest fix. Use when e2e fails locally or in CI, or before merging a feature to develop.
---

# Run and debug the Kind end-to-end test

The workflow is `scripts/e2e-kind.sh` (via `make e2e-kind`). It creates the Kind cluster
`kavrynt-alpha0` (override with `KIND_CLUSTER_NAME`), builds and loads local images, installs
the chart, runs the product loop, and deletes the cluster it created.

## Safety

- Never touch the user's current kube context or Kind clusters this script did not create.
  Pass `--context kind-<cluster>` on every `kubectl` call.
- If a cluster with the same name already exists, ask before deleting it.
- Use port-forwarding, not NodePort.

## 1. Run

```bash
docker info >/dev/null && kind version && helm version --short
make e2e-kind 2>&1 | tee /tmp/kavrynt-e2e.log
```

To keep the cluster for inspection after a failure: `KEEP_CLUSTER=1 make e2e-kind`.

For a CI failure instead: `gh run view <run-id> --log-failed -R kavrynt/kavrynt` (job
"Kind End-to-End").

## 2. Map the failure to the contract

| # | Check | Look at first |
| --- | --- | --- |
| 1 | Gateway and Operator ready | `kubectl get pods -n kavrynt-system`, events, image load (Apple Silicon: images must be built locally and `kind load`ed) |
| 2 | `MCPServer` reaches `Ready` | operator logs, `kubectl describe mcpserver -n kavrynt-e2e` |
| 3 | CRD rejects unsafe endpoints | CRD validation in both copies of `kavrynt.io_mcpservers.yaml` (`config/crd/bases/` and the chart's `k8s-operator/files/`) |
| 4 | Gateway RBAC read-only, no Secrets | `charts/kavrynt/charts/gateway/templates/` RBAC, `kubectl auth can-i --as=system:serviceaccount:kavrynt-system:kavrynt-gateway` |
| 5 | Route visible in Gateway and `kavryctl list` | `/v1/routes`, gateway logs (watch and cache sync) |
| 6 | `tools/list` through Gateway | `test/e2e/mcp-server`, `internal/gateway/routing`, proxy headers |
| 7 | `kavryctl register/inspect/unregister` | `internal/kavryctl/cli`, kubeconfig/context flags |
| 8 | Deleting `MCPServer` removes the route | gateway watch handling of delete events |
| 9 | Upgrade from the previous release and rollback (SPEC-0009) | `UPGRADE_FROM_VERSION` in `scripts/e2e-kind.sh` (published?), registry access for the Kind node, `charts/kavrynt/charts/k8s-operator/templates/crd.yaml`, Helm's ownership error in the output |

With `KEEP_CLUSTER=1`:

```bash
CTX=kind-kavrynt-alpha0
kubectl --context $CTX get pods,mcpservers -A
kubectl --context $CTX -n kavrynt-system logs deploy/kavrynt-operator --tail=200
kubectl --context $CTX -n kavrynt-system logs deploy/kavrynt-gateway --tail=200
kubectl --context $CTX get events -A --sort-by=.lastTimestamp | tail -40
```

## 3. Fix and re-run

Make the smallest fix, run the relevant unit tests (`go test -race ./internal/<component>/...`),
then `make e2e-kind` again. Finish with `kind delete cluster --name kavrynt-alpha0` if you kept it.

Report: which check failed, the root cause with file:line, the fix, and the passing re-run.
