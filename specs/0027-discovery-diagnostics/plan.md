# SPEC-0027 plan: Discovery diagnostics

## Design

- **Discovery errors.** `ListTools` returns a `*discovery.Error` for every
  failure. It carries a kind (HTTP status, connection, DNS, egress,
  timeout, not MCP, other), the MCP method, and the facts the message
  needs. `Error()` builds the message only from what the Operator knows:
  the host, port, and path of `spec.endpoint` (never its query), the HTTP
  status code, the egress kind, and the timeout. It never formats the
  wrapped transport error, whose text holds resolved addresses and the
  full URL, and never quotes the server (R11). Messages are cut to 512
  characters.
- **Classification.** In order: an egress refusal (`*egress.RefusedError`
  in the chain, R07); a timeout (`context.DeadlineExceeded` or a
  `net.Error` timeout, R09); a host that does not resolve
  (`*net.DNSError` with `IsNotFound`, R06); any other dial failure
  (`*net.OpError` with op `dial`, R05); then the answer: HTTP `404` to
  `initialize` (R04), `401` or `403` (R08), a response that is not
  `application/json` or `text/event-stream` or holds no matching JSON-RPC
  response (R10). Other failures keep today's terse messages.
- **Egress.** `egress.Control` returns `*egress.RefusedError{Kind}`, with
  the kinds `cloud metadata` (169.254.169.254, `fd00:ec2::254`,
  100.100.100.200), `link-local`, `loopback`, `unspecified`, and
  `multicast`. Its `Error()` names the kind and the address, as before, for
  Gateway logs; discovery messages use only the kind. The Gateway's answer
  to clients does not change (`upstream request failed`).
- **Controller.** Unchanged: it already sets `DiscoveryFailed` and
  `Message: err.Error()`, and never touches `Ready` (R03). Credential
  failures (`CredentialUnavailable`) are not discovery failures and keep
  their messages.
- **CRD.** A `Discovery` printer column on
  `.status.conditions[?(@.type=="ToolsDiscovered")].reason`, after `Ready`,
  in both copies (R01).
- **kavryctl.** `TOOLS` in `list` is the count when `ToolsDiscovered` is
  `True`, the reason when it is `False`, and `-` when it is absent (R02).

## Affected files

| Path | Change |
| --- | --- |
| `internal/egress/egress.go` | `RefusedError` with the address kind |
| `internal/operator/discovery/discovery.go`, `errors.go` | `Error`, classification, messages |
| `config/crd/bases/kavrynt.io_mcpservers.yaml`, `charts/kavrynt/charts/k8s-operator/files/kavrynt.io_mcpservers.yaml` | `Discovery` column |
| `internal/kavryctl/cli/cli.go` | `TOOLS` shows the reason |
| `scripts/e2e-kind.sh` | Column, messages, and `kavryctl list` for the failing servers |
| `docs/components/operator/README.md`, `docs/components/kavryctl/README.md`, `docs/runbooks/`, `docs/TESTING.md` | Conditions, messages, troubleshooting |

## Verification

| Requirement | Test |
| --- | --- |
| SPEC-0027-R01 | `internal/operator/crd_test.go` or chart test: both CRD copies have the column; `e2e-kind.sh` `kubectl get` shows `DiscoveryFailed` |
| SPEC-0027-R02 | `cli_test.go` `TOOLS` for True, False, and absent; `e2e-kind.sh` `kavryctl list` |
| SPEC-0027-R03 | `mcpserver_controller_test.go` every failure kind keeps `DiscoveryFailed` and `Ready` |
| SPEC-0027-R04 to R10 | `discovery_test.go` one case per cause, with fake round trippers |
| SPEC-0027-R07 | `egress_test.go` kinds; `e2e-kind.sh` metadata endpoint message |
| SPEC-0027-R11 | `discovery_test.go` no body, header, query, or address in any message; length bound |

## Rollout

Additive, no flag. Existing servers get the new messages on their next
discovery attempt. Scripts that parse `kavryctl list` see a reason in
`TOOLS` where they saw `-` (release notes).

## Risks

- A message that leaks an address or server text: R11 tests feed errors
  that contain addresses, query strings, and server bodies, and check none
  appears.
- Misclassification sends users the wrong way: each cause has its own
  test, and unknown failures fall back to today's messages.
