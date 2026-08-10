# Kavrynt

Kavrynt is an MCP infrastructure control plane for platform teams. It provides
the core components needed to register, route, operate, and eventually govern
MCP servers across local development and Kubernetes environments.

This repository is the public product monorepo for the Kavrynt MVP.

## Components

```text
cmd/kavryctl        Developer and operator CLI
services/registry  Registry API and metadata source of truth
services/gateway   Runtime gateway for MCP traffic
operator           Kubernetes operator for MCPServer custom resources
```

Each component is currently kept as an independent Go module. The root
`go.work` file connects them for local development.

## Repository Layout

```text
.
├── cmd/
│   └── kavryctl/
├── services/
│   ├── gateway/
│   └── registry/
├── operator/
├── .github/
│   └── workflows/
├── AGENTS.md
├── Makefile
├── go.work
└── README.md
```

## Local Development

Prerequisites:

- Go 1.23+
- Docker, for image builds
- Helm, for chart validation
- kubectl and Kind, for local Kubernetes testing

Run Go checks across all components:

```bash
make qa
```

Run only tests:

```bash
make test
```

Build all local development images:

```bash
make docker-build
```

Validate Helm charts:

```bash
make helm-lint
make helm-template
```

## MVP Flow

The first end-to-end Kavrynt workflow is:

```text
Developer or platform engineer
  -> defines an MCP server manifest or MCPServer custom resource
  -> registers it through kavryctl or the Kubernetes operator
  -> Registry stores the MCP server metadata
  -> Gateway syncs Registry state
  -> MCP clients call the server through Gateway
```

## Source Repositories

This monorepo was assembled from the original private component repositories:

- `kavryctl`
- `registry`
- `gateway`
- `k8s-operator`

Those repositories should be considered pre-monorepo component sources once this
repository becomes the primary public development location.

## License

License selection is pending. Do not assume permissive reuse rights until a
license file is added.
