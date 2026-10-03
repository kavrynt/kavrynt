# ADR-0002: Remove The In-Cluster Registry

## Status

Accepted (2026-10-02). Implemented on `develop` for release `0.0.2-beta.1`
(not yet published).

## Context

In `0.0.1-beta.1` the data path is:

```text
MCPServer CR -> operator -> Registry HTTP API (file store) -> gateway polls every 10 s
kavryctl -> Registry HTTP API
```

The registry duplicates state the Kubernetes API already stores durably. It
adds a deployment whose file store sits on an `emptyDir` volume (all routes are
lost when the pod restarts, until the operator re-syncs), an unauthenticated
write API, and a 10-second polling delay before routes change. Cross-cluster inventory is
provided by Kavrynt Cloud's hosted registry, so the in-cluster copy has no
unique role.

## Decision

Remove the registry component. The Kubernetes API, through the `MCPServer`
custom resource and its status, is the in-cluster source of truth.

| Concern | `0.0.1-beta.1` | `0.0.2-beta.1` |
| --- | --- | --- |
| Desired state | `MCPServer` CR | `MCPServer` CR (unchanged) |
| Validation | Registry HTTP API | CRD OpenAPI schema and CEL validation rules |
| Operator | Pushes manifests to registry | Validates, resolves endpoint, writes status conditions (`Ready`, `Accepted`) |
| Gateway routes | Polls registry | Watches `MCPServer` with an informer; reads only `Ready` servers |
| `kavryctl list/inspect` | Registry HTTP API | Kubernetes API through the user's kubeconfig |
| `kavryctl register` | Registry HTTP API | Applies an `MCPServer` resource |
| Cross-cluster view | None | Operator reports inventory to Kavrynt Cloud (later release) |

### Gateway RBAC

The gateway gets `get`, `list`, `watch` on `mcpservers.kavrynt.io` and nothing
else. It never writes resources. Scope is cluster-wide by default, with a chart
option to restrict to listed namespaces through namespaced Roles.

### API changes to `kavrynt.io/v1alpha1`

- Status: remove `registrySyncedAt` and `registryError`; replace the
  `Registered` condition with `Accepted` and `Ready`.
- Spec: `endpoint` is validated at admission (absolute `http`/`https` URL,
  no embedded credentials, at most 2048 characters).
- Deferred to a later release to keep this change narrow: a `remote` spec for
  MCP servers outside the cluster with SSRF allow-listing, and `secretKeyRef`
  replacing plain-text `environment` values.
- The API stays `v1alpha1`; breaking changes are allowed before `v1beta1` and
  are listed in the upgrade notes.

### Migration for trial users

`0.0.1-beta.1` was never published, so no upgrade path from it is supported.
The temporary migration code and upgrade test were removed on 2026-10-03.

## Consequences

- One fewer deployment, image, writable API, and file store per cluster.
- Route changes propagate on watch events instead of a 10-second poll.
- The gateway needs Kubernetes API access, so it runs with its own service
  account and minimal RBAC.
- `kavryctl` local file-registry commands are removed; `kavryctl` becomes a
  Kubernetes and Kavrynt Cloud client.
- Customer documentation and the website must change in the same release.
  They keep describing `0.0.1-beta.1` until `0.0.2-beta.1` images are
  published and verified.
- Useful validation logic in `registry/internal/validation` is ported to CRD
  validation and operator checks before the registry code is deleted.
