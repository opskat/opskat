<!-- Copy into e2e/scratch/<scenario>/ as report.md before running. Headings stay English; write the record in the user's language. Delete unused sections and this comment. -->

# Local verification: <scenario>

## Mode

`verifying a change` | `reproducing a bug`

## Goal / problem

<Expected observable behaviour and risk, or Expected/Actual bug statement>.

## Environment

<!-- What the run drove, so a reader can tell a harness run from a `make dev` one. -->

- Form and entry point: `<opsctl / dev-sandbox + drive.mjs / scratch spec on the harness / packaged binary>`
- Data directory and port: `<the sandbox's ~/.opskat-verify and :34220, the harness's temp dir and :34216, or the throwaway --data-dir>`
- Build under test: `<commit sha, and dev build vs packaged binary>`

## Verdict

<!-- Fill last. Keep verdicts only here. One row per claim — split a compound claim rather than averaging it. Where `not observed` came from unconfigured environment, "How observed" names the service and the absent variable names, never values. -->

| # | Requirement / bug claim | Verdict | Real / substituted | How observed | Check it yourself |
|---|---|---|---|---|---|
| V1 | `<one behaviour or bug claim, stated so it can only be true or false>` | holds / does not hold / not observed | real, or `substituted: <what stood in> — <what it does not cover>` | `<the runtime observation that decides it>` | `<command, or launch command plus steps>` |

Summary: <what holds, the deciding observation, every not-observed/failed item and shipping implication>.

| Label | Use it when | Requires |
|---|---|---|
| `holds` | you observed the behaviour at runtime | the deciding observation, and how a reader reaches it |
| `does not hold` | you observed it failing, or the bug reproducing | the failing output, assertion diff or error screenshot |
| `not observed` | you never reached the check | what stopped it |

An unreached check is never `holds`; a run that verified two of three claims is reported as two of three.

## Authorization

<!-- Keep only when a real dependency was substituted or an external effect was authorized. The in-harness redis-mock, ssh-mock and openai-mock are substitutions; so is seeding an asset that points at a real `.env` target. -->

| # | Substitute or effect | The user's authorization, verbatim |
|---|---|---|
| V1 | `<what stood in for what, or the effect and what it touches>` | `<sentence>` |

## Reproduction steps

<!-- Keep for bug reproduction; state whether the assertion encodes the expected behaviour (stays red) or the current buggy contract (green until the fix flips it). -->

1. `<clean-checkout-to-observation steps>`

## Acceptance evidence

<!-- One `###` per verdict row, holding everything that decides it in the order observed. No verdict labels here. A row with no section is `not observed`. -->

### V1 · `<the claim, restated>`

```console
$ <command>   # cwd and relevant redacted environment
<deciding lines>
$ echo $?
0
```

<What this proves>. Full output: `logs/<file>`.

<!-- The independent read that corroborates it — an `audit_logs` row, a structured log line, or a read-only query. A UI assertion alone does not prove the write landed. -->

```text
<audit_logs row or log line, redacted>
```

<!-- UI only; pair before/after or light/dark in one table so the comparison is one glance. -->

| Before | After |
|---|---|
| `![before](v1-before.png)` | `![after](v1-after.png)` |

## Evidence index

Everything lives in this scenario directory (`e2e/scratch/<scenario>/`).

- Commands/logs: `<inline deciding output plus optional full-file links>`
- GUI actions: `drive.log` — every `drive.mjs` call and its outcome, appended as it ran
- Resources/data snapshots: `<paths and what each proves>`
- Screenshots/video: `<UI only; video includes decisive stills>`

## Persistent data changes

<!-- Keep only when authorized persistent data changed — a real `.env` target, a migration against real rows, a seeded asset in your own inventory. The harness temp data directory is not one. -->

| Change | Forward | Backward/backup | Before/after query |
|---|---|---|---|
| `<scope/blast radius>` | `<command/exit>` | `<command/exit or irreversible plan>` | `<evidence>` |

## Execution record

| Step | Status | Evidence/blocker |
|---|---|---|
| `<step>` | pending / passed / failed / blocked | `<path or observation>` |

## Integrity and cleanup

- Initial/final HEAD: `<sha>` / `<sha>`
- Final `git status --porcelain=v1`: `<output>`
- Created artifacts, processes, seeded assets and external data, and how each was cleaned up: `<inventory>`
- Redaction performed: `<what was removed>`

## Evidence rules

- Every `holds` names how the target was driven — command, or launch command plus steps — and the deciding observation.
- Where a claim changes state beyond the driven surface, that observation is an independent read with its own command and exit code; a harness run's evidence is copied here **while the run is alive**, because the runner deletes the temp data directory on exit.
- Embed decisive text and images inline; scrolling this file should reach a verdict without opening a side file. Link only archives, binaries and full captures, each with a note on what it holds.
- Paste terminal output as text. Screenshotting a terminal manufactures evidence instead of capturing it.
- Keep failed and unchecked steps visible. Redact credentials, tokens, hostnames of real targets and personal data before saving, and again before embedding.
- Keep every path relative to this file; the scenario directory, not `report.md` alone, is what you hand to a reviewer.
