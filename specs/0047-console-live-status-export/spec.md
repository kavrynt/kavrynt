# SPEC-0047: Console data freshness, export, and copy link

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-11-20 |
| Issue | [#192](https://github.com/kavrynt/kavrynt/issues/192) |
| ADRs | none |
| Supersedes | none |

## Summary

An operator can leave the console open, but cannot tell how old what they
see is, cannot refresh on demand, and cannot take the data with them. This
spec shows when each page last refreshed, adds a refresh control and a clear
"reconnecting" state when the Gateway does not answer, adds client-side
download of the lists the console already shows (servers, tools, traffic,
alerts), and a control that copies the current page's link.

## Problem

The console polls (servers every 5 s, traffic every 2 s, history every 30 s,
and stops while the page is hidden, SPEC-0002-R14). A failed poll shows
"Gateway unreachable" in the sidebar and "Cannot load servers" on the Servers
page, but no page says how old its data is, there is no way to refresh now,
and a person who wants the tool list for a review or an incident ticket has
to copy it by hand. Filters are already in the URL (SPEC-0002-R10), but
nothing offers the link.

## Goals

- Show on every data page when its data was last refreshed, and make an
  outage obvious without hiding the last data.
- Refresh now, and pause and resume polling, from one place.
- Download the lists on screen as CSV or JSON, exactly as shown.
- Copy the current page's link in one action.

## Non-goals

- New data or new API endpoints: exports are built in the browser from the
  responses the page already holds.
- Scheduled or server-side reports, or export of anything the console does
  not show (tool arguments, results, tokens, credentials, condition
  messages beyond what is displayed).
- Cluster-wide history (Prometheus and Grafana, SPEC-0023).

## Requirements

### Freshness

- **SPEC-0047-R01** Every page that shows polled data MUST show, in its
  header, when that data was last refreshed ("Updated 4 s ago"), updating at
  least every 5 s, as text (not colour alone) and in a polite live region
  only when the state changes between fresh, stale, and failed, never on
  every tick.
- **SPEC-0047-R02** Data MUST count as stale when its last successful
  refresh is older than three poll intervals of its endpoint. A stale page
  MUST say so in its header ("Stale: last updated 1 min ago") and keep
  showing the last data.
- **SPEC-0047-R03** When a poll fails, the console MUST show a banner
  naming what failed and that it is retrying, retry with exponential backoff
  from the poll interval up to 30 s, and clear the banner and say
  "Reconnected" after the next success. The banner MUST NOT contain the
  response body, a URL with a query, or an address; only the endpoint's
  purpose ("servers", "traffic") and the HTTP status or "no answer".
- **SPEC-0047-R04** A Refresh button in the page header MUST fetch that
  page's data now and report the result as in R01 and R03. A Pause control
  MUST stop and resume all polling; while paused the header MUST say
  "Paused" and the age keeps counting. Pause MUST NOT survive a page reload.
  The Traffic page's existing Pause (which freezes the table) stays and MUST
  be the same control, not a second one.

### Export and link

- **SPEC-0047-R05** The Servers list, the Tools catalog, the Traffic request
  table, the Traffic per-tool table, and the Alerts list MUST each offer
  "Download CSV" and "Download JSON" that contain exactly the rows and
  columns shown after the current filters and sort (SPEC-0046), plus a header
  row (CSV) and the page's replica and time window (JSON `meta`).
- **SPEC-0047-R06** CSV MUST follow RFC 4180 and MUST neutralize formula
  injection: a field that begins with `=`, `+`, `-`, `@`, tab, or carriage
  return MUST be prefixed with a single quote. JSON MUST be valid and carry
  the same values as strings or numbers as displayed.
- **SPEC-0047-R07** An export MUST NOT contain anything the page does not
  show: no tool arguments, results, tokens, `Authorization` values,
  credentials, environment values, or condition messages that the table does
  not display. The Traffic export MUST say it covers one Gateway replica and
  the last 500 requests, as the page does (SPEC-0002-R09).
- **SPEC-0047-R08** Downloads MUST be generated in the browser from data
  already loaded, without a new request, and MUST NOT write to
  `localStorage`, `sessionStorage`, IndexedDB, or cookies (SPEC-0002-R13).
- **SPEC-0047-R09** A "Copy link" control MUST copy the page's current URL
  (including filters, sort, and range) and confirm with a message that
  screen readers announce, and MUST say so when copying fails.

### Tests

- **SPEC-0047-R10** Age and staleness, backoff, CSV and JSON building, and
  formula neutralization MUST be DOM-free functions that `make console-test`
  covers. The browser smoke test MUST show the stale and the reconnecting
  state by stopping the Gateway route, and download one export, at 360 px and
  1280 px.

## Acceptance criteria

- Given the Servers page open for 20 s, when polling is healthy, then the
  header reads "Updated" with an age of 5 s or less (R01).
- Given the Gateway stops answering, when three intervals pass, then the
  page says stale with the age, shows a retrying banner without any address,
  and keeps the last rows; when it answers again, "Reconnected" appears
  (R02, R03).
- Given a server whose description starts with `=cmd`, when the Servers list
  is downloaded as CSV, then that field reads `'=cmd` (R06).
- Given the Tools catalog filtered to one server and sorted by name, when
  "Download JSON" is pressed, then the file holds those rows in that order
  and no schemas, arguments, or hints beyond the columns (R05, R07).
- Given Traffic, when Pause is pressed on any page, then polling stops
  everywhere and the header says "Paused" until Resume or reload (R04).

## Security and privacy

The files hold data the signed-in user already sees; they are saved where the
user chooses and leave the console's control. Spreadsheet formula injection
through server names, tool names, and descriptions (untrusted `MCPServer` and
MCP metadata) is neutralized (R06). Nothing is sent to the Gateway or any
other service to build a file, and the banner never repeats response bodies
or addresses (R03). The console login still gates every endpoint the data
comes from.

## Compatibility

Console only. No API, CRD, Helm, or flag change; the new controls are
additive. Polling intervals are unchanged.

## Documentation

`docs/components/gateway/README.md` (console section), once `Implemented`.

## Open questions

- Should the export include the alert's runbook link as shown? (maintainer)
- Is "three poll intervals" the right staleness threshold for the 30 s
  history poll (90 s)? (maintainer)
