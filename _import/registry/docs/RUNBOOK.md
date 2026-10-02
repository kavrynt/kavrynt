# Registry First-Time Runbook

This runbook explains how to validate Kavrynt Registry locally, with Docker,
with Helm, and in GitHub Actions.

## Prerequisites

- Go 1.23 or newer
- Git
- curl
- Docker, for image validation
- Helm 3, for chart validation
- A Kubernetes cluster, only if you want to run the Helm chart

## Branching Workflow

Kavrynt follows GitFlow:

```bash
git switch develop
git pull
git switch -c feature/<short-branch-name>
```

After work is complete and tested:

```bash
git status --short
git diff --stat
git add .
git commit -m "Short clear message"
git push -u origin feature/<short-branch-name>
```

Open a pull request into `develop`. Merge to `main` only for production-ready
release code.

## Local Validation

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
```

## Local API Smoke Test

Start the server:

```bash
GOCACHE="$PWD/.cache/go-build" go run . --addr :8080 --data /tmp/kavrynt-registry.json
```

In another terminal:

```bash
curl -sS http://localhost:8080/healthz
curl -sS http://localhost:8080/readyz
curl -sS http://localhost:8080/version
curl -sS -X POST http://localhost:8080/v1/servers \
  -H 'Content-Type: application/json' \
  --data-binary @examples/mcp-server.json
curl -sS http://localhost:8080/v1/servers
curl -sS http://localhost:8080/v1/servers/example-mcp-server
curl -i -X DELETE http://localhost:8080/v1/servers/example-mcp-server
```

Cleanup:

```bash
rm -f /tmp/kavrynt-registry.json
```

## Docker Build

```bash
docker build \
  --build-arg VERSION=0.1.0-dev \
  --build-arg COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t kavrynt/registry:dev .
```

Run:

```bash
docker run --rm -p 8080:8080 kavrynt/registry:dev
```

## Helm Validation

```bash
helm lint charts/registry
helm template registry charts/registry
```

## Helm Run in Kubernetes

Install:

```bash
helm install registry charts/registry \
  --set image.repository=kavrynt/registry \
  --set image.tag=dev
```

Check:

```bash
kubectl get pods -l app.kubernetes.io/name=registry
kubectl get svc
```

Cleanup:

```bash
helm uninstall registry
```

## GitHub Actions QA

The QA workflow validates:

- Go formatting
- Go tests
- Go vet
- Go vulnerability scanning
- binary build
- API smoke test
- Docker image build
- Helm lint
- Helm template rendering
