# SPEC-0049: Console polish: loading states, shortcuts, command palette, empty states

| Field | Value |
| --- | --- |
| Status | Draft |
| Owners | @Ravi041 |
| Created | 2026-11-20 |
| Issue | [#194](https://github.com/kavrynt/kavrynt/issues/194) |
| ADRs | none |
| Supersedes | none |

## Summary

The console is accessible and complete for its first purpose, but it
feels like a developer tool next to the products operators compare it with.
This spec lists the smallest set of changes that close the gap without a
build step or an external asset: skeleton placeholders while data loads,
keyboard shortcuts and a command palette to jump to a page, server, or tool,
one consistent shape for empty states, an Overview that leads with what needs
attention, and a compact density for large installs.

## Problem

First load shows a plain "Loading…" line and then the page jumps as content
arrives. There is no way to move between pages or find a server without the
pointer and the sidebar. Empty states range from helpful ("Call a tool from
the Playground or point your agent at a Gateway route", Traffic) to a bare
line ("No active alerts."). The Overview presents five equal tiles above the
one card that says whether anything needs attention. Installs with many
servers get the same roomy spacing as a demo.

## Goals

- Content-shaped placeholders instead of "Loading…", with no layout jump.
- Move around and find things from the keyboard.
- Every empty state says why it is empty and what to do next.
- The problem that needs attention comes first.
- An optional compact density.

## Non-goals

- A build step, a framework, web fonts, icon fonts, or any external asset
  (the console rules in CLAUDE.md stay in force).
- New data, new Gateway endpoints, or hosted features (those belong in
  `kavrynt-cloud`).
- Customizable dashboards, saved views, or user preferences beyond theme
  and density.

## Requirements

### Loading

- **SPEC-0049-R01** While a page's first data is loading, the page MUST show
  placeholder blocks the size of the content they stand for, marked
  `aria-busy="true"` on the region, with an accessible "Loading" status, and
  MUST replace them without moving the content below. Once loading has
  failed, the page MUST show the error state (SPEC-0047-R03), not
  placeholders.
- **SPEC-0049-R02** Placeholder animation MUST stop under
  `prefers-reduced-motion: reduce`, and placeholders MUST meet the console's
  contrast rules in both themes.

### Keyboard

- **SPEC-0049-R03** The console MUST offer these shortcuts, all inactive
  while focus is in a text field, select, or editable region, or when a
  modifier other than the stated one is held: `/` focuses the page's search
  box when it has one; `g` then `o`, `d`, `s`, `t`, `p`, `r`, `a` go to
  Overview, Dashboard, Servers, Tools, Playground, Traffic, Alerts; `?` opens
  a list of the shortcuts. Shortcuts MUST NOT replace any browser or screen
  reader command, and MUST be listed in the `?` dialog and in the docs.
- **SPEC-0049-R04** `Ctrl+K` or `Cmd+K` (and a visible "Search" control in the
  header) MUST open a command palette: a dialog with a labelled search box
  that lists pages, servers (by `namespace.name`), and tools (by name and
  server) matching what is typed, in a stable order, navigable with the
  arrow keys, chosen with Enter, closed with Escape, with focus trapped in
  the dialog and returned to the opener. Names come from the loaded
  `MCPServer` and tool metadata (untrusted) and MUST be inserted as text
  only.
- **SPEC-0049-R05** The palette and the shortcut list MUST be usable with a
  screen reader (dialog role, labelled, results announced as a listbox with
  the active option exposed) and at 360 px.

### Empty states and hierarchy

- **SPEC-0049-R06** Every list or table that can be empty MUST show an empty
  state with a title, one sentence saying why it is empty, and, where one
  exists, the next action as a link or button ("Register a server", "Open the
  Playground"). An empty state that exists today and says this MUST be kept.
- **SPEC-0049-R07** The Overview MUST list, above the summary tiles, what
  needs attention (servers not routed, failing discovery, active alerts) when
  there is anything, and the tiles otherwise stay as they are. The "none" case
  stays as today.
- **SPEC-0049-R08** The console MUST offer a Compact density (smaller
  vertical padding in tables and cards, not smaller text), chosen in the
  header menu, kept in `localStorage` as one word, and applied before first
  paint to avoid a jump. It MUST NOT reduce any control below the target
  size set by the accessibility rules.

### Tests

- **SPEC-0049-R09** Shortcut parsing (including the "inactive in a text field"
  rule), palette matching and ordering, and the density choice MUST be
  DOM-free functions that `make console-test` covers. The browser smoke test
  MUST open the palette, jump to a server, open the shortcut list, and check
  the placeholders on a throttled first load, at 360 px and 1280 px.

## Acceptance criteria

- Given a slow first load of Servers, when the page opens, then card-shaped
  placeholders show with a "Loading" status, and the real cards replace them
  without the header moving (R01).
- Given focus on the page, when `g` then `s` is pressed, then Servers opens;
  given focus in the search box, when `g` is typed, then it is just a letter
  (R03).
- Given servers `team-sre.incidents` and `team-platform.runbooks`, when
  `Ctrl+K` opens the palette and `inc` is typed, then `team-sre.incidents`
  is the first result and Enter opens its page (R04).
- Given no alerts, when Alerts opens, then it says why and what an alert
  would look like, not just "No active alerts." (R06).
- Given a server not routed, when the Overview opens, then the server and the
  reason appear above the tiles (R07).

## Security and privacy

No new data is read, sent, or stored beyond one density word in
`localStorage`. Names shown in the palette are untrusted and inserted with
`textContent` only. Shortcuts never trigger an action that changes cluster
state; navigation only.

## Compatibility

Console only. No API, CRD, Helm, or flag change. Existing URLs are
unchanged.

## Documentation

`docs/components/gateway/README.md` (console section, a shortcut table), once
`Implemented`.

## Open questions

- Which of these should ship first? Placeholders and empty states are small;
  the palette is the largest. (maintainer)
- Does a per-page help link into docs.kavrynt.com belong here, or in a
  separate spec once the docs have stable per-page anchors? (maintainer)
