# Kavrynt Gateway Runbook

This runbook validates the Gateway MVP locally and in Kubernetes.

## Local Validation

Run static checks:

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
helm lint charts/gateway
helm template gateway charts/gateway
```

Start Registry in another terminal:

```bash
cd ../registry
go run . --addr :8081 --data /tmp/kavrynt-registry.json
```

Register an HTTP MCP server through `kavryctl`:

```bash
cd ../kavryctl
go run . register --registry http://localhost:8081 ../gateway/examples/mcp-server.json
```

Start Gateway:

```bash
cd ../gateway
go run . --registry-url http://localhost:8081 --addr :8080
```

Check readiness and routes:

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
curl -fsS http://localhost:8080/v1/routes
curl -fsS http://localhost:8080/metrics
```

## Expected MVP Behavior

- `/readyz` returns `200` after Gateway successfully syncs Registry once.
- `/v1/routes` lists registered MCP servers.
- `/mcp/<server-name>` proxies to the server's registered HTTP endpoint.
- `stdio` transports are rejected with `502` in this MVP.

## Docker

Build and run:

```bash
docker build -t kavrynt/gateway:dev .
docker run --rm -p 8080:8080 \
  -e KAVRYNT_REGISTRY_URL=http://host.docker.internal:8081 \
  kavrynt/gateway:dev
```

## Kubernetes With Helm

Render:

```bash
helm template gateway charts/gateway \
  --set config.registryURL=http://registry.default.svc.cluster.local:8080
```

Install:

```bash
helm install gateway charts/gateway \
  --set config.registryURL=http://registry.default.svc.cluster.local:8080
```

Port-forward:

```bash
kubectl port-forward svc/gateway-gateway 18080:8080
curl -fsS http://localhost:18080/readyz
curl -fsS http://localhost:18080/v1/routes
```

Uninstall:

```bash
helm uninstall gateway
```

## Troubleshooting

If `/readyz` returns `503`, check:

- `--registry-url` or `KAVRYNT_REGISTRY_URL` is set.
- Registry is reachable from Gateway.
- Registry `GET /v1/servers` returns valid JSON.
- NetworkPolicy, service names, or port-forwarding are not blocking traffic.

If proxy requests return `404`, check:

- the MCP server name in `/mcp/<server-name>` matches `metadata.name`,
- Gateway has synced recently,
- `/v1/routes` contains the expected route.

If proxy requests return `502`, check:

- the registered server uses `spec.transport: http`,
- `spec.endpoint` is reachable from Gateway,
- the upstream MCP server is healthy.
