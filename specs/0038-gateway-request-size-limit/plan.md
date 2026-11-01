# SPEC-0038 plan: Request size limit for MCP routes

## Decisions

- 4 MiB by default: well above ordinary MCP calls, small enough for MCP
  servers with a 128 MiB memory limit, and the same order as common RPC
  defaults.
- Refuse on `Content-Length` before anything is read, so an oversized
  request never reaches the server. Bodies without a length are streamed
  through `http.MaxBytesReader`, not buffered, so the limit bounds the
  server's input without adding Gateway memory per request.
- The check runs after authentication and route lookup, so it never
  reveals which routes exist; client authentication reads at most
  `maxInspectBytes` of a form body before it.

## Design

`Handler.maxRequestBytes` (default `DefaultMaxRequestBytes`, set with
`SetMaxRequestBytes`) bounds `r.Body` with `http.MaxBytesReader` at the
start of `Handler.proxy`. After the route checks, a `Content-Length` over
the limit is refused (`refuseTooLarge`). A body that grows past the limit
while it is read, in `inspectRequest` or while it is forwarded, ends with
an `*http.MaxBytesError`; `countingReader` keeps the first read error so
`proxy` answers `413` for it instead of `400` or `502`.

## Affected files

| Path | Change |
| --- | --- |
| `internal/gateway/api/api.go` | Limit, `SetMaxRequestBytes`, `refuseTooLarge` |
| `internal/gateway/api/traffic.go` | `countingReader` keeps its read error |
| `internal/gateway/api/size_test.go` | Tests |
| `cmd/gateway/main.go` | `--max-request-bytes` |
| `charts/kavrynt/charts/gateway/*` | `config.maxRequestBytes`, schema, argument |
| `charts/kavrynt/tests/*` | Chart tests |
| Docs | Chart README, Gateway component docs |

## Verification

| Requirement | Test |
| --- | --- |
| R01 | `api.TestOversizedContentLengthIsRefusedBeforeTheServer` |
| R02 | `api.TestOversizedChunkedBodyIsCutAndRefused` |
| R03 | `api.TestRequestSizeLimitDefaultAndSetting`, chart `gateway_test.yaml`, `values_schema_test.yaml` |
| R04 | `api.TestOversizedRequestIsRecordedAsClientError` |

## Rollout

On by default in the next release; see the spec.

## Risks

Clients that send large tool arguments (files, images) get `413` until an
operator raises the limit. The error message names the flag and the chart
value.
