# SPEC-0025: Approvals for tool calls

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-10-26 |
| Issue | to be opened |
| ADRs | [ADR-0003](../../docs/ADR-0003-Runtime-Authorization.md) |
| Supersedes | none |

## Summary

The tool authorization policy (SPEC-0012) can answer that a tool call needs
approval. The Gateway then holds the call and asks an approval authority to
get a person's decision through OpenID Client-Initiated Backchannel
Authentication (CIBA). It forwards the call only when the decision is to
approve, and refuses it when the decision is to deny or the request
expires. Kavrynt Cloud is one such approval authority. The customer's own
authorization server is another, when it supports CIBA.

## Problem

Policy today can only allow or deny. Teams want agents to use tools that
act on production, such as deleting a record, refunding a payment, or
merging a change, but only after a person agrees to that specific call.
Without approvals they either deny those tools to agents or allow them
unattended. ADR-0003 names `require_approval` as a policy outcome and
leaves the flow to this spec.

## Goals

- A policy can require a person's approval for a tool call, decided per
  call, without the agent or the MCP server changing.
- The approval is bound to the exact call: caller, client, route, tool, and
  arguments. It cannot be replayed for another call.
- Approvals use a standard protocol (CIBA), so they work with Kavrynt Cloud
  and with authorization servers that support CIBA.
- Every approval, denial, and expiry is in the audit events (SPEC-0013).

## Non-goals

- The approver's user interface, notification channels, and approver
  groups. They belong to the approval authority: Kavrynt Cloud
  (`kavrynt-cloud`) or the customer's authorization server.
- Approving in the Gateway console. The console stays read-only, and its
  single admin login is not an approver identity.
- Approvals for anything but `tools/call`.
- Standing grants such as "approve this tool for an hour". Each approval
  covers one call (see Open questions).

## Requirements

### Policy decision

- **SPEC-0025-R01** Approvals MUST be off unless
  `gateway.approvals.enabled=true`. Turning them on MUST need tool
  authorization policy (SPEC-0012) and MCP client authentication
  (SPEC-0006); the Gateway MUST refuse to start without both.
- **SPEC-0025-R02** A `tools/call` whose policy decision has
  `require_approval` equal to `true` MUST NOT be forwarded until it is
  approved, even when the decision also has `allow` equal to `true`. A
  decision may name the approver in `approver` (a string of at most 256
  characters); without one, the approver is the caller (`subject`).
- **SPEC-0025-R03** A decision that requires approval for a method other
  than `tools/call` MUST deny the request, as an invalid decision does
  (SPEC-0012-R07).

### Approval request

- **SPEC-0025-R04** The Gateway MUST ask for approval with a CIBA
  backchannel authentication request (OpenID CIBA Core 1.0, poll mode) to
  the authority at `gateway.approvals.issuer`. It finds the endpoints in
  the authority's metadata, authenticates with `client_secret_basic` (the
  client secret comes from a kubelet-mounted Secret), and sends `scope`
  `kavrynt:approve`, the approver as `login_hint`, and a `binding_message`.
- **SPEC-0025-R05** The `binding_message` MUST name the route, the tool,
  and a short code that is also returned to the client. It MUST NOT
  contain tool arguments (but see Open questions). The policy's `reason`
  MUST be sent as `kavrynt_reason`, at most 200 characters with control
  characters removed.
- **SPEC-0025-R06** The request MUST bind the approval to the call: the
  Gateway MUST send `kavrynt_call`, the SHA-256 of the caller's subject,
  client ID, route, tool, and the canonical JSON of the tool arguments,
  and MUST forward the call only when the token issued on approval carries
  the same `kavrynt_call`, the approval authority as issuer, and the
  Gateway's client ID as audience. Each approval MUST be used for one call
  only.
- **SPEC-0025-R07** The Gateway MUST poll the token endpoint at the
  interval the authority returns (default 5 seconds), never faster, and
  MUST slow down on `slow_down`.

### The held call

- **SPEC-0025-R08** While approval is pending, the Gateway MUST hold the
  client's request for up to `gateway.approvals.wait` (default 60 seconds,
  at most the request timeout). Approved within it, the call MUST be
  forwarded and answered as usual.
- **SPEC-0025-R09** When the authority answers `access_denied`, the call
  MUST get `403` with a JSON-RPC error `approval denied`. When the request
  expires (`expired_token`), it MUST get `403` with `approval expired`. In
  neither case may it reach the MCP server.
- **SPEC-0025-R10** When `gateway.approvals.wait` passes with approval
  still pending, the call MUST get `202` with a JSON-RPC error
  `approval pending`, the short code, and `Retry-After`. The same call
  repeated by the same caller before the request expires MUST wait for
  that request rather than start a new one.
- **SPEC-0025-R11** When the authority cannot be reached or answers with
  an error other than those above, the call MUST be refused with `503`
  (`approval_unavailable`) and not forwarded: approvals fail closed.

### Records

- **SPEC-0025-R12** The Gateway MUST record the outcomes `approved` (with
  the call's own outcome), `approval_denied`, `approval_expired`,
  `approval_pending`, and `approval_unavailable` in the traffic metrics,
  `/v1/traffic`, and the console. Audit events MUST carry the decision
  `require_approval`, the approver, the short code, and the outcome, and
  MUST NOT carry tool arguments, the CIBA request ID, or tokens.
- **SPEC-0025-R13** Tokens issued on approval, the client secret, and the
  CIBA request ID MUST NOT appear in logs, metrics, `/v1/*` answers, or
  audit events.

### Chart and end to end

- **SPEC-0025-R14** The chart MUST expose `gateway.approvals.enabled`,
  `issuer`, `clientID`, `clientSecretKey` (in the Secret named by
  `gateway.auth.tokenExchangeSecret` or its own `secretName`), and `wait`,
  validated by the schema.
- **SPEC-0025-R15** In the e2e, the test authorization server MUST serve
  CIBA. A policy MUST require approval for one tool, and the test MUST
  show an approved call reaching the server, a denied call refused with
  `403`, a pending call answered with `202` and later approved on retry,
  and audit events for each without arguments or tokens.

## Acceptance criteria

- Given a policy that requires approval for `refund` with approver
  `finance-oncall`, when an agent calls `refund`, then the approver gets a
  request naming the route, the tool, and a code, and the agent's call
  completes once they approve (R02, R04, R05, R08).
- Given the approver denies it, then the agent gets `403 approval denied`
  and the MCP server never sees the call (R09).
- Given an approval for `refund` with `{"amount": 10}`, when the agent
  calls `refund` with `{"amount": 1000}`, then that call needs its own
  approval (R06).
- Given the approval authority is down, then calls that need approval get
  `503` and calls that do not need it are unaffected (R11).

## Security and privacy

The approval authority sees the caller's subject, the route, the tool, the
policy's reason, and a hash binding the arguments, never the arguments
themselves (R05, R06). The approver therefore approves a tool and context,
not argument values; Open questions asks whether that is enough. The
Gateway gains outbound access to the approval authority, through the egress
policy (`internal/egress`) used for issuers. A held call ties up a
connection for up to `wait`; the number of held calls per replica is
bounded (see the plan). Every failure refuses the call.

## Compatibility

New values under `gateway.approvals`, new outcomes, and a new policy
decision field. Off by default. Existing policies are unchanged, since
`require_approval` is a new field. No CRD change.

## Documentation

Gateway component doc (approvals, outcomes, audit fields), chart README,
example policies (an approval example), the e2e contract in `CLAUDE.md`,
`ROADMAP.md` (item 1 of "Next"), and the ADR-0003 status line.

## Open questions

- **Who approves?** Recommended: the policy names the approver (`approver`,
  sent as CIBA `login_hint`), and the caller approves their own call when
  it does not. A separate approver catches a compromised agent; the
  caller's own consent catches an agent acting beyond the user's intent.
  Owner decides.
- **Kavrynt Cloud only, or any CIBA authority?** ADR-0003 says the
  decision is made in Kavrynt Cloud. Recommended: any authority that speaks
  CIBA, with Kavrynt Cloud as one of them, so self-hosted installations
  can use Keycloak or another server that supports CIBA. Kavrynt Cloud's
  CIBA endpoint is specified in `kavrynt-cloud`.
- **How does the client wait?** Recommended: hold the request (R08), then
  answer `202 approval pending` and match the retry (R10). MCP Tasks
  (long-running `tools/call` with a task handle) would fit better once
  clients support them widely, and can replace R10 then. Owner decides
  whether to wait for Tasks instead.
- **Should the approver see the arguments?** Recommended for this spec:
  no. Arguments are customer data the Gateway never records. A later
  per-tool opt-in can send selected arguments in `binding_message`.
- **Pending approvals across replicas.** R10 matches a retry on the
  replica that holds the request; a retry on another replica starts a
  second request, which the approver sees as a duplicate. Recommended:
  accept for the first version and document it, rather than give the
  Gateway shared state or write access to Kubernetes.
- **Standing grants.** One approval per call (R06) is safest but noisy for
  repeated calls. A time-boxed grant per caller and tool can be proposed
  separately.
