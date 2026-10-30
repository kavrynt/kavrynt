## What and why

<!-- What does this change do, and why? Link the issue: "Fixes #123". -->

## Spec

<!-- specs/NNNN-name and the requirement IDs this PR covers, or "None: bug fix / refactor / docs / dependency". See specs/README.md. -->

## How it was tested

- [ ] `make qa` passes
- [ ] `make e2e-kind` passes, or the change cannot affect the product loop
- [ ] New behavior has tests that run without network access and cite their requirement IDs

## Checklist

- [ ] Every commit is signed off (`git commit -s`)
- [ ] Docs and README describe only what the code actually does
- [ ] CRD changes are made in both CRD copies and in `api/v1alpha1`
- [ ] Helm value changes are in `values.schema.json`, the chart README values tables, and `charts/kavrynt/tests/`
- [ ] Spec status and the `specs/README.md` index are updated, if a spec is involved
- [ ] Every CodeRabbit comment is answered: fixed, or replied to with the reason it does not apply
