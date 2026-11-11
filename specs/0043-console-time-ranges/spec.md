# SPEC-0043: Console dashboard time ranges

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-11-11 |
| Issue | [#159](https://github.com/kavrynt/kavrynt/issues/159) |
| ADRs | none |
| Supersedes | none (refines SPEC-0024-R04, R05, and R06) |

## Summary

The console's Dashboard page offers only the last hour, 6 hours, or 24
hours. This spec adds the last 5, 15, and 30 minutes and the last 4 hours,
and a custom time range anywhere inside the last 24 hours, for operators who
need to look at an incident that started minutes ago or ended hours ago. The
Gateway's history is unchanged: still in memory, still 24 hours at most.
Ranges beyond 24 hours (2, 7, 15, and 30 days) stay in Grafana and
Prometheus (SPEC-0023), and the page says so.

## Problem

An operator who sees errors "just now" must read a one-hour chart whose
interesting part is the last few pixels, and cannot isolate "between 14:10
and 14:40 yesterday" at all. SPEC-0024 fixed three ranges to keep the first
version small; there is no way to choose another window, and the page does
not tell the operator where longer history lives.

## Goals

- Choose the last 5, 15, or 30 minutes, 1, 4, 6, or 24 hours, or a custom
  window inside the last 24 hours, for all servers, one server, or one tool.
- Share a custom window with a link that carries its exact start and end.
  A preset link (`range=15m`) stays relative: it means "the last 15
  minutes" for whoever opens it.
- Say where 2 to 30 days of history are, instead of offering ranges the
  Gateway cannot serve.

## Non-goals

- History beyond 24 hours, or history that survives a Gateway restart. The
  Gateway keeps its bounded in-memory history (SPEC-0024-R02, Decisions);
  2, 7, 15, and 30 days belong to Prometheus and the Grafana dashboards
  (SPEC-0023). Serving them from the console, by querying Prometheus or by
  persisting rollups, needs its own spec and a security review of the new
  outbound access or stored data.
- Cluster-wide numbers summed over replicas (SPEC-0024 Decisions).
- Finer resolution than one minute, or a refresh interval or live tail.

## Requirements

### API

- **SPEC-0043-R01** `GET /v1/history` MUST accept `range` values `5m`,
  `15m`, `30m`, `1h`, `4h`, `6h`, and `24h`. `1h`, `6h`, and `24h` MUST
  answer as before (SPEC-0024-R04), and without `range` or `from` the range
  stays `1h`.
- **SPEC-0043-R02** `GET /v1/history` MUST accept a custom window as `from`
  and `to`, both RFC 3339 timestamps, instead of `range`. A `to` later than
  now MUST be treated as now. Stored points cover whole steps (R03), so the
  Gateway MUST round `from` down and `to` up to the step boundary, never
  beyond the retained history or the newest point, and MUST NOT scale or
  split a point to fit. The answer MUST name `range` as `custom` and carry
  `start` and `end` as the rounded window the points cover, which can be
  wider than the one asked for by less than two steps (a 14:11 to 14:36
  window at five-minute steps answers as 14:10 to 14:40).
- **SPEC-0043-R03** The points MUST be one minute apart when the window
  starts within the last hour and five minutes apart otherwise, so the
  answer never claims a resolution the history does not have. `stepSeconds`
  MUST say which.
- **SPEC-0043-R04** The Gateway MUST refuse with `400` and a stable JSON
  error: `range` together with `from` or `to`; only one of `from` and `to`;
  a timestamp that is not RFC 3339; and, checked after `to` is clamped to
  now (R02), `from` not before `to` (which includes a `from` in the future),
  a window shorter than five minutes, and a `from` earlier than 24 hours
  before now. Any
  other `range` value, including `2d`, `7d`, `15d`, and `30d`, MUST stay
  refused (SPEC-0024-R05), and the error for those MUST say that the
  Gateway keeps 24 hours.
- **SPEC-0043-R05** The history the Gateway keeps and its memory bound
  (SPEC-0024-R01, R02) MUST NOT change, and answering a window MUST stay a
  copy of that window's points (SPEC-0024-R03).

### Console

- **SPEC-0043-R06** The Dashboard page's range selector MUST offer the last
  5 minutes, 15 minutes, 30 minutes, 1 hour, 4 hours, 6 hours, and 24 hours,
  and Custom. The server's Dashboard tab (SPEC-0024-R07) MUST offer the
  same.
- **SPEC-0043-R07** Choosing Custom MUST show a start and an end, in the
  browser's time zone, and an Apply control. The console MUST refuse a
  window the API would refuse (R04), with a message beside the inputs and
  without a request. When Apply is pressed, the console MUST send the window, and once the
  answer arrives MUST store the answer's effective `start` and `end` (R02,
  after clamping and rounding) in the URL as UTC `from` and `to`, so a shared
  link carries the window its creator saw and an end in the future does not
  move. The page MUST show the effective `start`, `end`, and `stepSeconds` of
  the answer, also when they are wider than the window entered. When it opens
  a link and the answer's `start`, `end`, or `stepSeconds` differ from the
  link's `from` and `to` (the Gateway keeps one-minute points for only the
  last hour, R03), it MUST say so. A preset is kept in the URL as `range` and
  stays relative to when the link is opened. A link with an invalid or
  unknown range or window MUST fall back to `1h`. A link whose
  window has aged past the 24 hours the Gateway keeps MUST also show `1h`,
  but with a visible message that the window has expired, naming its
  requested start and end.
- **SPEC-0043-R08** The page MUST say that the Gateway keeps 24 hours and
  that 2, 7, 15, and 30 days are in Grafana when a metrics backend with
  enough retention is configured (SPEC-0023): links when
  `gateway.console.grafanaURL` is set, otherwise the dashboards' names
  (SPEC-0024-R10). It MUST NOT offer a range of more than 24 hours that
  the Gateway refuses.
- **SPEC-0043-R09** For every range, the chart's time axis labels, the
  per-chart table of numbers, the "calls in range" tile, and the alert marks
  (SPEC-0024-R08, R09) MUST describe the shown window, and a window with
  no points MUST say so instead of drawing empty charts.
- **SPEC-0043-R10** Range definitions, custom-window validation, and the URL
  round trip MUST be DOM-free functions that `make console-test` covers, and
  window handling in the history endpoint Go code that `make test` covers.
  The browser smoke test MUST render the Dashboard with a short range and a
  custom window at 360 px and 1280 px without page errors or horizontal
  scrolling (SPEC-0024-R12).

## Acceptance criteria

- Given errors that started 8 minutes ago, when the operator selects the
  last 15 minutes, then the chart shows 15 one-minute points and the
  error-rate rise is visible, and the 5-minute range shows 5 points
  (R01, R03, R06).
- Given an incident between 14:10 and 14:40 yesterday, when the operator
  enters that window as a custom range, then the chart shows 6 points five
  minutes apart, the URL carries it as `from` and `to`, and opening the
  link in another browser shows the same window (R02, R03, R07).
- Given a custom window that starts 25 hours ago, when it is applied, then
  the console shows a message beside the inputs and sends no request; the
  same request sent to `/v1/history` answers `400` (R04, R07).
- Given `GET /v1/history?range=7d`, then the Gateway answers `400` saying it
  keeps 24 hours, and the console offers no 7-day option but points to
  Grafana (R04, R08).
- Given a Gateway replica restarted 20 minutes ago, when the last hour is
  selected, then the charts start 20 minutes ago as in SPEC-0024 (R05, R09).

## Security and privacy

`from`, `to`, and `range` are untrusted query parameters. The Gateway parses
them strictly, bounds the window to the retained 24 hours so a request
cannot make it copy more than one range's points, and answers refused values
with a fixed error that does not echo them. The console reads them from the
URL as text, validates them, and never inserts them as HTML. The endpoint
stays behind the console login and returns the same data classes as
SPEC-0024-R04. No new outbound access, Kubernetes permissions, or stored
data.

## Compatibility

Additive on `GET /v1/history`: new `range` values and `from`/`to`; existing
values and answers are unchanged. No Helm value, CRD, or memory change. The
Dashboard page's URLs with `range=1h|6h|24h` keep working. Once this spec is
`Implemented`, the range lists in SPEC-0024-R04, R05, and R06 refer here.

## Documentation

Gateway component doc (the `/v1/history` row and the console dashboards
section), chart README (console), the console section of the README, and the
testing runbook (`docs/TESTING.md`) where it opens the console.

## Open questions

- Should `6h` stay in the selector next to `4h`? Kept here so existing links
  and habits work; the owner may drop it. Decides: owner.
- Should a custom window be entered in the browser's time zone (this spec) or
  UTC? Browser time matches the axis labels (SPEC-0024-R08); the URL is UTC
  either way. Decides: owner.
- Long ranges (2 to 30 days) in the console: a follow-up spec choosing
  between querying Prometheus and persisting rollups. Not decided here.
