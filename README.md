# Kavrynt Gateway

Kavrynt Gateway is the MVP data-plane entry point for HTTP MCP traffic.

It syncs registered MCP server metadata from Kavrynt Registry, builds an
in-memory route table, and proxies requests from:

```text
/mcp/<server-name>
```

to the registered HTTP endpoint for that server.

## MVP Scope

Included:

- Registry sync from `GET /v1/servers`.
- In-memory route table.
- HTTP transport proxying for registered MCP servers.
- Health, readiness, version, metrics, and route inspection endpoints.
- Docker, GitHub Actions QA, Helm chart, and runbook.

Not included yet:

- authentication and authorization,
- tenant isolation,
- Gateway-to-Registry mTLS,
- stdio MCP process execution,
- Kubernetes Operator integration,
- durable route cache,
- advanced MCP protocol validation.

## Run Locally

Start Registry first, then run Gateway:

```bash
go run . --registry-url http://localhost:8081 --addr :8080
```

Environment variable alternative:

```bash
KAVRYNT_REGISTRY_URL=http://localhost:8081 go run .
```

Useful endpoints:

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
curl -fsS http://localhost:8080/version
curl -fsS http://localhost:8080/v1/routes
curl -fsS http://localhost:8080/metrics
```

Proxy a registered HTTP MCP server:

```bash
curl -fsS -X POST http://localhost:8080/mcp/example-mcp-server \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

## Validate

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
helm lint charts/gateway
helm template gateway charts/gateway
```

## Docker

```bash
docker build -t kavrynt/gateway:dev .
docker run --rm -p 8080:8080 \
  -e KAVRYNT_REGISTRY_URL=http://host.docker.internal:8081 \
  kavrynt/gateway:dev
```

## Private GHCR Image

Kavrynt private release images are published to GitHub Container Registry:

```text
ghcr.io/kavrynt/gateway:<tag>
```

For local publishing, authenticate with a classic GitHub token that has
`write:packages`:

```bash
export CR_PAT=<classic-token-with-write-packages>
echo "$CR_PAT" | docker login ghcr.io -u <github-username> --password-stdin
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=0.1.0-beta \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t ghcr.io/kavrynt/gateway:0.1.0-beta \
  -t ghcr.io/kavrynt/gateway:beta \
  --push .
```

The package is private by default on first publish. Link it to this private
repository and inherit repository permissions from the package settings.

## Helm

```bash
helm install gateway charts/gateway \
  --set config.registryURL=http://registry.default.svc.cluster.local:8080 \
  --set imagePullSecrets[0].name=ghcr-kavrynt
```
