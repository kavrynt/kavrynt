# kavryctl First-Time Runbook

This runbook is for a first-time Kavrynt contributor validating `kavryctl`
locally, in Docker, in GitHub Actions, and with Helm.

## Prerequisites

- Go 1.23 or newer
- Git
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

## Local Go Validation

From the repository root:

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go run . version
GOCACHE="$PWD/.cache/go-build" go run . validate examples/mcp-server.json
```

## Local CLI Workflow

Use a temporary home directory so local state is easy to delete:

```bash
export KAVRYNT_HOME=/tmp/kavrynt-local
GOCACHE="$PWD/.cache/go-build" go run . init
GOCACHE="$PWD/.cache/go-build" go run . register examples/mcp-server.json
GOCACHE="$PWD/.cache/go-build" go run . list
GOCACHE="$PWD/.cache/go-build" go run . inspect example-mcp-server
GOCACHE="$PWD/.cache/go-build" go run . unregister example-mcp-server
```

Cleanup:

```bash
rm -rf /tmp/kavrynt-local
```

## Remote Registry Workflow

Start Kavrynt Registry from the `registry` repository:

```bash
cd /Users/ravindrakumar/Desktop/Kavrynt/Github-Code/registry
GOCACHE="$PWD/.cache/go-build" go run . --addr :8080 --data /tmp/kavrynt-registry.json
```

In another terminal, use `kavryctl`:

```bash
cd /Users/ravindrakumar/Desktop/Kavrynt/Github-Code/kavryctl
export KAVRYNT_REGISTRY_URL=http://localhost:8080
GOCACHE="$PWD/.cache/go-build" go run . register examples/mcp-server.json
GOCACHE="$PWD/.cache/go-build" go run . list
GOCACHE="$PWD/.cache/go-build" go run . inspect example-mcp-server
GOCACHE="$PWD/.cache/go-build" go run . unregister example-mcp-server
```

Equivalent explicit form:

```bash
GOCACHE="$PWD/.cache/go-build" go run . register --registry http://localhost:8080 examples/mcp-server.json
GOCACHE="$PWD/.cache/go-build" go run . list --registry http://localhost:8080
GOCACHE="$PWD/.cache/go-build" go run . inspect --registry http://localhost:8080 example-mcp-server
GOCACHE="$PWD/.cache/go-build" go run . unregister --registry http://localhost:8080 example-mcp-server
```

Cleanup:

```bash
rm -f /tmp/kavrynt-registry.json
```

## Docker Build

Build the CLI image:

```bash
docker build -t kavrynt/kavryctl:dev .
```

Build with version metadata:

```bash
docker build \
  --build-arg VERSION=0.1.0-dev \
  --build-arg COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t kavrynt/kavryctl:dev .
```

Smoke test the image:

```bash
docker run --rm kavrynt/kavryctl:dev version
docker run --rm -v "$PWD/examples:/examples:ro" kavrynt/kavryctl:dev validate /examples/mcp-server.json
```

## Helm Validation

Render and lint the chart:

```bash
helm lint charts/kavryctl
helm template kavryctl charts/kavryctl
```

## Helm Run in Kubernetes

The current chart runs a Kubernetes Job that executes `kavryctl version`.
It does not deploy Gateway, Registry, or Operator services yet.

Install:

```bash
helm install kavryctl charts/kavryctl \
  --set image.repository=kavrynt/kavryctl \
  --set image.tag=dev
```

Watch the Job:

```bash
kubectl get jobs
kubectl get pods -l app.kubernetes.io/name=kavryctl
kubectl logs job/kavryctl-kavryctl
```

Cleanup:

```bash
helm uninstall kavryctl
```

## GitHub Actions QA

The QA workflow runs on:

- pushes to `feature/**`
- pushes to `develop`
- pushes to `main`
- pull requests targeting `develop` or `main`

It validates:

- Go formatting
- Go tests
- Go vet
- Go vulnerability scanning with `govulncheck`
- Go binary build
- CLI smoke test
- remote Registry client tests
- Docker image build
- Helm lint
- Helm template rendering

## Troubleshooting

If Go cannot write to the default build cache, set `GOCACHE`:

```bash
mkdir -p .cache/go-build
export GOCACHE="$PWD/.cache/go-build"
```

If Docker is not running, start Docker Desktop and rerun the Docker commands.

If Helm cannot connect to a cluster, run only:

```bash
helm lint charts/kavryctl
helm template kavryctl charts/kavryctl
```
