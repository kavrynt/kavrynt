# SPEC-0042 tasks

Each pull request is test-first, and its tests cite the requirements it verifies.

## Pull request 1: streams (R01, R02, R09)

1. Characterization tests that show a `GET` event stream outliving its token. The
   final assertions cover R01 and R02: the stream ends by the expiry plus `clockSkew`
   with `token_expired`, and an upstream disconnect stays `incomplete`.
2. `clientauth.Identity` carries the token's expiry.
3. The proxy ends the stream at the expiry plus `clockSkew`, with outcome
   `token_expired`.
4. Alert tests show `token_expired` is not a failure, in the chart and the console.
5. `scripts/e2e-kind.sh`, `docs/E2E.md`, and `CLAUDE.md`: a stream ends at expiry.

## Pull request 2: sessions (R03 to R08, R10, R11)

1. A characterization test that shows another caller's session ID being accepted. The
   final assertion covers R04: `404`, with no request to the server.
2. `Authenticate` refuses a token with neither subject nor client ID.
3. `mcpsession.Bind` and `Open`, with the key loaded by `--session-key-file`.
4. The proxy checks the tag before the pod tag, and adds it after the pod tag.
5. The chart's Secret, mount, flag, value, schema, and unit tests.
6. `scripts/e2e-kind.sh` (another caller's session, another replica, a rolling restart
   with client authentication on), docs, and removing both known limitations from
   `SECURITY.md`.
7. Set the spec to `Implemented`.
