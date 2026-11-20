# SPEC-0048: Console Playground: inline validation and re-running calls

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-11-20 |
| Issue | [#193](https://github.com/kavrynt/kavrynt/issues/193) |
| ADRs | none |
| Supersedes | none |

## Summary

The Playground works end to end, but it tells people about a missing required
field with a short-lived message at the edge of the window, does not check
raw JSON-RPC before sending it, does not preselect the only routed server,
and keeps a history it cannot use. This spec adds inline field errors, JSON
checking before a raw call is sent, preselection of a single server, and
restoring, re-running, and copying a history entry as a `curl` command.
Nothing leaves the page and nothing is stored.

## Problem

Today a call with an empty required field shows "Missing required: query.
Click again to call anyway." as a toast; it fades, sits far from the field,
and needs a second click. Raw mode accepts any text and reports a problem
only after the Gateway answers. History lists the method and status of past
calls in this session but cannot be reopened, so repeating a call means
retyping its arguments. Before a server is chosen, the Tools card shows a
bare "…".

## Goals

- Show what is wrong at the field that is wrong, to everyone.
- Catch malformed raw requests before they are sent.
- Repeat or adjust a previous call without retyping.
- Share a reproducible command without leaking a token.

## Non-goals

- Persisting history or presets across reloads (SPEC-0002-R13 keeps
  Playground history in memory).
- Schema-complete form generation for nested or union types beyond what the
  Playground renders today; "Edit as JSON" remains the escape hatch.
- Calling servers that are not routed, or any change to what the Gateway
  receives.

## Requirements

### Forms

- **SPEC-0048-R01** When a call is attempted with a required field empty, the
  Playground MUST show an error beside each such field ("query is required"),
  mark the field `aria-invalid="true"` and `aria-describedby` that message,
  and move focus to the first one. The message MUST remain until the field
  has a value or the tool changes.
- **SPEC-0048-R02** The Playground MUST still allow calling with a required
  field empty, as an explicit second action ("Call anyway") shown with the
  errors, not by pressing the same button again, because servers may
  document defaults. The toast MUST be dropped.
- **SPEC-0048-R03** When exactly one routed server exists and none is chosen
  in the URL, the Playground MUST select it. With none routed, the Tools card
  MUST say that no server is routed and link to Servers; before a choice is
  made with several, it MUST say "Choose a server to list its tools" and MUST
  NOT show "…".

### Raw mode

- **SPEC-0048-R04** Before sending a raw request the Playground MUST parse it
  and refuse to send when it is not valid JSON, showing the parser's message
  with the line and column of the error beside the editor and focus on it.
  A body that is valid JSON but not a JSON-RPC 2.0 request or notification
  (missing `jsonrpc: "2.0"` or `method`) MUST be refused with a message that
  names what is missing. The Gateway's own answers (SPEC-0044) are unchanged.
- **SPEC-0048-R05** Raw mode MUST offer a "Format" action that re-indents
  valid JSON and does nothing, with a message, when it is not valid.

### History

- **SPEC-0048-R06** Selecting a history entry MUST restore that call's tool
  (or raw mode) and its arguments into the form or editor, ready to edit, and
  show its recorded response. "Run again" on an entry MUST send the same
  request once, as a new entry.
- **SPEC-0048-R07** "Copy as curl" on a history entry or the current form
  MUST copy a `curl` command for the call: the route URL, the JSON body, and
  the headers `Content-Type` and `Accept`. Every value (URL, body, header
  values) MUST be a single-quoted shell word with each `'` written as
  `'\''`, so nothing in a raw body, a route, or a session id (`$(...)`,
  backticks, `$VAR`, quotes, newlines) is executed or expanded when the
  command is pasted. When the Gateway requires access tokens (`mcpAuth`), the
  command MUST carry `Authorization: Bearer $ACCESS_TOKEN` in a double-quoted
  word that holds only that literal text, and MUST NOT contain a pasted token
  or any session cookie. If the request body contains the pasted token (the
  text of the access token field), the console MUST refuse to copy and say
  why, rather than copy or silently alter the body. A session id MUST be
  included only as the `Mcp-Session-Id` header the call used.
- **SPEC-0048-R08** History MUST stay in memory only: nothing from R06 or R07
  is written to `localStorage`, `sessionStorage`, IndexedDB, or cookies
  (SPEC-0002-R13), and signing out clears it (SPEC-0005-R14).

### Tests

- **SPEC-0048-R09** Field validation, raw-request checking with error
  position, and `curl` building MUST be DOM-free functions that
  `make console-test` covers, including tests that no output contains a
  token, that bodies, routes, and session ids holding shell metacharacters
  stay inside their quotes, and that a body holding the token is refused. The browser smoke test MUST trigger an inline error, a raw JSON
  error, and a re-run at 360 px and 1280 px.

## Acceptance criteria

- Given `search_runbooks` with `query` empty, when Call tool is pressed, then
  "query is required" appears under the field, focus moves to it, no toast
  appears, and "Call anyway" is offered (R01, R02).
- Given the raw editor containing `{ "method": "tools/list"`, when Send is
  pressed, then nothing is sent and the message names the line and column
  (R04).
- Given one routed server, when `#/playground` is opened, then that server is
  selected and its tools are listed (R03).
- Given a finished call, when its history entry is selected and "Run again"
  is pressed, then a second entry appears with the same arguments (R06).
- Given a Gateway that requires tokens and a token pasted into the
  Playground, when "Copy as curl" is used, then the text contains
  `$ACCESS_TOKEN` and not the token (R07).

## Security and privacy

Tool arguments are typed by the signed-in user and stay in the page. They are
never stored (R08) and never logged. The copied `curl` command never holds a
token or cookie (R07); an operator pastes their own. Everything the Playground
shows from a server (descriptions, schemas, results) is untrusted and is
inserted as text only.

## Compatibility

Console only. No API, CRD, Helm, or flag change. The Gateway sees the same
requests as before; invalid raw bodies are now refused in the browser, and
the Gateway's refusals (SPEC-0044) still apply to anything sent by other
clients.

## Documentation

`docs/components/gateway/README.md` (Playground section), once `Implemented`.

## Open questions

- Should "Call anyway" stay at all, or should required fields be strictly
  enforced? (maintainer)
- Should a response be diffable against the previous run of the same call?
  (follow-up, not part of this spec)
