# SPEC-0046: Console tables: sorting, paging, and a server list view

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-11-20 |
| Issue | [#191](https://github.com/kavrynt/kavrynt/issues/191) |
| ADRs | none |
| Supersedes | none |

## Summary

The console's tables show data in the order the Gateway sends it and cannot
be reordered or paged, and the Servers page offers cards only. This spec adds
sortable columns and paging to the Tools catalog and the Traffic tables, a
result count beside every filter, a list view for servers beside the cards,
and the full endpoint with a copy control. Filters already live in the URL
(SPEC-0002-R10); sort order and page size join them.

## Problem

An operator with dozens of servers and hundreds of tools cannot find "the
slowest tool" or "the server with the most errors" without exporting
metrics. Traffic keeps the last 500 requests of a replica, shows 100 and a
"Show all" button that draws the rest at once; the Tools catalog draws every
tool. Server cards truncate the endpoint with no way to read or copy it, and
leave much of a wide screen empty. Filters and "Clear filters" already work
and are not part of this spec.

## Goals

- Click or press a column header to sort the Tools catalog and the Traffic
  tables, and see which column and direction is active.
- Page long tables instead of drawing every row.
- Say how many rows match the filters.
- Read and copy a server's full endpoint, and scan servers in a dense list.

## Non-goals

- Server-side sorting or paging: the Gateway still sends what it sends
  (`GET /v1/servers`, `GET /v1/traffic`), and no endpoint changes.
- Saved views, column choice, or export (SPEC-0047).
- New data on any table.

## Requirements

### Tables

- **SPEC-0046-R01** The Tools catalog, the Traffic "Per tool" table, and the
  Traffic "Requests" table MUST sort by a column when its header is activated
  with the pointer, Enter, or Space. A sortable header MUST be a button
  inside the `th`, and the `th` MUST carry `aria-sort` (`ascending`,
  `descending`, or `none`).
- **SPEC-0046-R02** Activating the sorted column's header MUST reverse its
  direction. Sorting MUST be stable: rows that compare equal keep their
  order. Numbers MUST sort as numbers, durations by value, and times by
  instant, not as text.
- **SPEC-0046-R03** The default order MUST be the one the table has today
  (the Tools catalog by tool name, Traffic newest first), so an unsorted view
  does not change.
- **SPEC-0046-R04** The sort column and direction MUST be kept in the URL
  hash query as `sort` (a column key, prefixed with `-` for descending)
  beside the existing filters, restored when the URL is opened, and ignored
  when the key is not a column of that table. A page with two tables gives
  each its own keys, so both states survive in one URL: the Traffic
  "Requests" table uses `sort`, `page`, and `size`, and its "Per tool" table
  uses `toolSort`, `toolPage`, and `toolSize` (R05).
- **SPEC-0046-R05** A table of more than 50 rows MUST show 50 at a time (this
  replaces the Traffic table's 100 rows and "Show all"), with
  Previous and Next controls, the visible range ("51 to 100 of 212"), and a
  page-size choice of 25, 50, or 100. The page and size MUST be kept in the
  URL under the table's own keys (R04); a page past the end MUST show the
  last page.
- **SPEC-0046-R06** Beside each filtered table the console MUST show how many
  rows match ("12 of 212 tools") and, when none match, say so and offer
  "Clear filters" (which exists today).
- **SPEC-0046-R07** The Traffic "Requests" table MUST keep its live behavior:
  with no sort chosen, new rows still appear within the poll
  interval, newest first. With a sort chosen, the visible page MUST NOT
  change on its own: within the poll interval the console MUST show "N new
  requests" with a control, and the new rows join the table, in the chosen
  order, only when the operator uses that control.

### Servers

- **SPEC-0046-R08** The Servers page MUST offer a Cards and a List view,
  chosen with a labelled control, kept in the URL as `view` and remembered per
  browser in `localStorage` (SPEC-0002-R13 forbids storing only tool
  arguments, results, and JSON-RPC messages). The list MUST show name,
  namespace, state, transport, version, tool count, and traffic, and sort
  as in R01 to R04.
- **SPEC-0046-R09** Wherever a server's endpoint is shown truncated, the
  console MUST give the full text to keyboard and pointer users (focusable
  control or tooltip with the full value) and a Copy control. The endpoint is
  text from the `MCPServer` and MUST be inserted as text only.

### Tests

- **SPEC-0046-R10** Sorting, paging, and the URL round trip MUST be DOM-free
  functions that `make console-test` covers. The browser smoke test MUST
  sort a table by keyboard and page one at 360 px and 1280 px without page
  errors or horizontal page scrolling.

## Acceptance criteria

- Given 212 tools, when the operator opens Tools, then 50 rows show, "1 to 50
  of 212" is visible, and Next shows the following 50 (R05).
- Given the Tools table, when the operator focuses "Server" and presses Enter
  twice, then rows run Z to A, the header says `aria-sort="descending"`, and
  the URL carries `sort=-server` (R01, R02, R04).
- Given a URL with `sort=bogus`, when it is opened, then the table shows its
  default order without an error (R04).
- Given Traffic sorted by latency, when a request arrives, then the visible
  rows do not move and "1 new request" appears within the poll interval;
  when its control is used, the row appears in latency order (R07).
- Given the Per tool table sorted by calls and the Requests table by
  latency, when the URL is copied and opened, then both sorts are restored
  (R04).
- Given two servers, when the operator picks List, then both show on one line
  each with the full endpoint available on focus and a Copy control (R08, R09).

## Security and privacy

Sorting and paging reorder and slice data the page already holds; no new
request, header, or field is added. Server names, endpoints, tool names, and
descriptions are untrusted and are inserted with `textContent`. The remembered
view choice is a single word in `localStorage`; no identity or data is stored.

## Compatibility

Console only. No API, CRD, Helm, or flag change. Existing URLs keep working;
new query keys (`sort`, `page`, `size`, `view`) are additive.

## Documentation

`docs/components/gateway/README.md` (console section) and the console
screenshots, once `Implemented`.

## Open questions

- Is a client-side page of 50 right for Traffic, which holds 500 rows at
  most? (maintainer)
- Should the list view be the default for installs with more than a set
  number of servers? (maintainer)
