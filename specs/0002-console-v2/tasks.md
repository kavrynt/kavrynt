# SPEC-0002 tasks: Console v2: operations UI

Proposed order once the spec is `Accepted`. Each task is one pull request.

- [ ] 1. Decide the browser test tooling (spec open question) and add it to
      CONTRIBUTING.md and CI.
- [ ] 2. Split `app.js` into ES modules with no behavior change; SPEC-0001
      tests and the Kind console checks stay green. Add the size budget test.
      (SPEC-0002-R18)
- [ ] 3. URL-encoded filters and visibility-aware polling.
      (SPEC-0002-R10, SPEC-0002-R14)
- [ ] 4. Storage scan test and in-memory Playground history; no-login notice.
      (SPEC-0002-R12, SPEC-0002-R13)
- [ ] 5. Traffic: method, tool, and outcome filters, per-tool summary, outcome
      definitions, replica scope note.
      (SPEC-0002-R06, SPEC-0002-R07, SPEC-0002-R08, SPEC-0002-R09)
- [ ] 6. Tools catalog with Playground links. (SPEC-0002-R04, SPEC-0002-R05)
- [ ] 7. Overview as the default view; diagnostics copy on server detail.
      (SPEC-0002-R01, SPEC-0002-R02, SPEC-0002-R03, SPEC-0002-R11)
- [ ] 8. Narrow layout, keyboard and contrast pass with the e2e smoke test.
      (SPEC-0002-R15, SPEC-0002-R16, SPEC-0002-R17)
- [ ] 9. Set status to `Implemented`; update the Gateway component doc,
      README, and `docs/DEMO.md`.
