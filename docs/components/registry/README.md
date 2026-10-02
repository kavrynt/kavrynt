# Kavrynt Registry

Kavrynt Registry is the control-plane source of truth for MCP server metadata,
versions, lifecycle state, and future policy references.

This first MVP slice provides a small HTTP API backed by a local JSON file. It
is intentionally dependency-light so we can validate the product shape before
choosing a production database or auth model.

## Current API

```text
GET    /healthz
GET    /readyz
GET    /version
GET    /metrics
POST   /v1/servers
GET    /v1/servers
GET    /v1/servers/{name}
DELETE /v1/servers/{name}
```

## Quick Start

```bash
mkdir -p .cache/go-build
GOCACHE="$PWD/.cache/go-build" go test ./...
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go run . --addr :8080 --data /tmp/kavrynt-registry.json
```

Register a server:

```bash
curl -sS -X POST http://localhost:8080/v1/servers \
  -H 'Content-Type: application/json' \
  --data-binary @examples/mcp-server.json
```

List servers:

```bash
curl -sS http://localhost:8080/v1/servers
```

Inspect a server:

```bash
curl -sS http://localhost:8080/v1/servers/example-mcp-server
```

Delete a server:

```bash
curl -i -X DELETE http://localhost:8080/v1/servers/example-mcp-server
```

## Development Model

Kavrynt uses GitFlow:

- `main` is production-ready code.
- `develop` is the latest integrated development branch.
- `feature/<branch-name>` is used for each focused feature.

Repository coding, branching, and security standards are defined in
[AGENTS.md](../../../AGENTS.md).

## Scope

In scope:

- MCP server metadata registration API.
- strict JSON manifest validation.
- file-backed local persistence.
- Docker image.
- Helm chart.
- CI QA pipeline.

Out of scope:

- authentication and authorization.
- policy enforcement.
- database-backed shared Registry.
- Gateway route generation.
- Kubernetes Operator synchronization.
- durable audit logging.

For first-time validation steps, see [docs/RUNBOOK.md](RUNBOOK.md).
