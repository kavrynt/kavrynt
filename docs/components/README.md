# Component Docs

Imported from the former `kavryctl`, `gateway`, `operator`, and `registry`
repositories (ADR-0001). Commands in these documents that use `go run .`,
component-level `charts/<name>`, or `examples/mcp-server.json` refer to the old
repository layout. In this repository use:

| Old | Now |
| --- | --- |
| `go run .` in a component repo | `go run ./cmd/<component>` |
| `charts/<component>` | `charts/kavrynt/charts/<component>` (`k8s-operator` for the operator) |
| `examples/mcp-server.json` | `examples/<component>/mcp-server.json` |
| `docker build .` | `docker build -f build/Dockerfile --target <component> .` |

The Registry documents are retained until the Registry is removed in
`0.0.2-beta.1` (ADR-0002).
