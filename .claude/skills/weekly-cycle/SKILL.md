---
name: weekly-cycle
description: Run Kavrynt's agent loop on one scoped task - build with kavrynt-coder, then kavrynt-reviewer and kavrynt-security, then kavrynt-tester, then fix findings - and hand the owner a short summary. Use for each feature or fix; the Friday pass uses it on the week's merged work.
argument-hint: <issue number, spec ID (SPEC-NNNN), or short task description>
disable-model-invocation: true
---

# Agent cycle for one task

Task: `$ARGUMENTS`. If it is empty, ask for it. Keep it to one logical change, small enough for one
pull request; if it is bigger, propose a split and stop.

The owner merges. Never commit, push, merge, or open a pull request unless the owner asks.

## 1. Build

Use the `kavrynt-coder` agent with the task. If it stops because a spec is `Draft` or missing,
report that and stop; the owner reviews specs.

## 2. Review and security, in parallel

Run `kavrynt-reviewer` and `kavrynt-security` at the same time on `git diff develop...HEAD` (include
uncommitted changes). Skip `kavrynt-security` only when the diff touches no Go, chart, CRD, console,
workflow, or Dockerfile files.

## 3. Fix

Pass the **blocker** and **should fix** findings, plus critical, high, and medium security findings,
back to `kavrynt-coder`. Verify each against the code first; reject findings that do not hold and say
why. Repeat step 2 once on the fixes. Do not loop a third time: report what remains.

## 4. Test

Run `kavrynt-tester`. Send real bugs back to `kavrynt-coder` once; then report.

## 5. Report

Give the owner: the change in two sentences, requirement IDs covered, each command run with its
result, findings fixed and findings rejected (with reasons), anything not verified, and the one
decision that is theirs. Remind them of the local `coderabbit review --agent --base develop` step
from `CLAUDE.md` before the first push.
