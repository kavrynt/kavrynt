# Alpha-0 Kind End-to-End Runbook

This runbook validates the canonical private Kavrynt runtime repositories as
one MCP Control Plane.

## Prerequisites

- Docker Desktop
- Kind
- kubectl
- Helm
- Go
- curl
- jq
- the sibling repositories `kavryctl`, `registry`, `gateway`, and
  `k8s-operator` under the same parent directory as this repository

## Run

From the private `kavrynt` integration repository:

```bash
make qa
make e2e-kind
```

The workflow builds local Apple Silicon-compatible images, creates a disposable
Kind cluster, installs the umbrella chart, and validates:

1. Operator reconciliation and the `Registered` condition.
2. Registry discovery.
3. Gateway route synchronization.
4. A JSON-RPC `tools/list` call through Gateway.
5. Registry and Gateway cleanup after deleting the `MCPServer`.

Expected final output:

```text
==> Alpha-0 end-to-end workflow passed
```

The cluster is deleted automatically. Keep it for troubleshooting:

```bash
KEEP_CLUSTER=1 make e2e-kind
```

Then inspect it:

```bash
kubectl --context kind-kavrynt-alpha0 get pods -A
kubectl --context kind-kavrynt-alpha0 get mcpservers -A
kind delete cluster --name kavrynt-alpha0
```

If the default cluster name already exists, use an isolated name:

```bash
KIND_CLUSTER_NAME=kavrynt-alpha0-2 make e2e-kind
```

## GitHub Actions

The integration workflow checks out the four private runtime repositories.
Configure the organization secret `KAVRYNT_REPO_TOKEN` with read-only access
to those repositories before enabling pull-request enforcement.
