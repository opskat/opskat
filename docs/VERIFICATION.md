# Verification — confirming a change actually works

Green unit tests prove the behaviours you asserted, not that the feature works. This route owns the order of verification and the one-off workflow; the mechanics live in the references — logs, DB and headless `opsctl` in [testing-debugging-guide.md](references/testing-debugging-guide.md), the GUI harness in [e2e-harness-guide.md](references/e2e-harness-guide.md).

## When to skip this route

Use targeted committed tests alone when they fully observe the changed logic — `service/`, `repository/`, `internal/ai/`, frontend stores and components. Use this route when real IPC, GUI, CLI or external wiring is needed, or when reproducing a runtime-only bug. It does not replace the [fix policy](../AGENTS.md#fix-policy--tdd-root-cause-in-scope): a reproduction confirms the bug is real and still owes the committed failing test.

Verification is not how the committed suite grows. Do not run `make test-e2e` to check one thing, and do not add an `e2e/tests/*.spec.ts` as part of it; promotion is a separate decision ([e2e-harness-guide.md](references/e2e-harness-guide.md#5-writing-a-committed-core-flow-spec)).

## The sandbox is the only thing you launch

```bash
make dev-sandbox          # start the real app on an isolated data dir, with a browser attached
```

It **returns** — nothing to keep in a foreground terminal — and it is idempotent, so calling it again just reports the running session. `make dev-sandbox-status` shows what is up; `make dev-sandbox-down` stops it.

Everything after that is an ad-hoc command against the live session — no spec file, no rerun, no rebuild between steps:

```bash
node e2e/drive.mjs snapshot                        # what is on screen right now
node e2e/drive.mjs click add-asset-button          # a bare word is a data-testid
node e2e/drive.mjs fill asset-form-name-input box1
node e2e/drive.mjs click asset-form-submit
node e2e/drive.mjs wait asset-form-dialog --hidden
node e2e/drive.mjs shot created                    # → e2e/scratch/<scenario>/created.png
node e2e/oracle.mjs assets box1                    # …and confirm it really hit the disk
```

`drive.mjs --help` and `oracle.mjs --help` list the full command set. The browser, the page and its state persist between commands, so you can look, act, look again, and change your mind — which is the point. Write a spec only for the third row of the table below.

**`snapshot` is how you avoid guessing.** It prints the visible structure with the `data-testid` to address each element by, plus roles, values and disabled state. Use a bare word for a testid; otherwise say which kind — `text=Save`, `role=button[name="保存"]`, `label=…`, `placeholder=…`, `css=…`. Bare words are testids, so raw CSS needs `css=`.

**Every command is recorded.** Each `drive.mjs` call appends one line — command, arguments, and outcome including failures and refusals — to `e2e/scratch/<scenario>/drive.log`. The sequence that produced a screenshot is captured as it happens rather than reconstructed from shell history afterwards.

**The browser is headless.** Driving it steals no focus and flashes no windows; `shot` and `snapshot` are how you see it. Pass `ARGS=--headed` when you want to watch. (The native Wails window that `wails dev` itself opens is separate and still appears — the app under test is a desktop app.)

**Data isolation is enforced, not assumed.** The sandbox boots with `OPSKAT_DATA_DIR` pointing at its own directory and `OPSKAT_E2E=1`, and `resolveBootstrap` in `main.go` refuses to start any run marked `OPSKAT_E2E=1` whose data dir resolves to your real one. So no verification can reach your live asset inventory, credentials, `master.key` or audit log, however it is invoked. `make dev` is the opposite — it *is* your real data directory, so it is for using the app, not for verifying against it.

Two guards, because they stop different things. The Go one stops a verification run from *booting* on real data; `drive.mjs` separately refuses to *drive* any URL that is not this checkout's own — so `open http://localhost:34115` (that is `make dev`, your real data) is rejected rather than silently obeyed.

**Worktrees run concurrently.** Ports, data dir, browser profile, logs and the session file are all keyed by a hash of the checkout path, and the block assignment is recorded in `~/.opskat-verify/workspaces.json`, so two worktrees each get a disjoint set and never see each other. `drive.mjs` and `oracle.mjs` resolve the session for *their own* checkout, so the same command means different things in different worktrees — which is what you want. `node e2e/oracle.mjs where` prints what the current one resolved to.

The sandbox data dir persists across launches; `make dev-sandbox ARGS=--reset` wipes it. `ARGS=--mocks` also starts the in-harness Redis / SSH / OpenAI mocks so Test Connection and the AI stack work with no real infrastructure.

## Workflow

1. Run `make lint` and the targeted tests; run `go test ./...` and `pnpm test` only when the blast radius is not confirmed local or a gate requires it ([DEVELOP.md](DEVELOP.md#common-commands)).
2. Start the drivable target. A real external dependency is reached through the gitignored `.env`, which the sandbox and `playwright.config.ts` load; configuration it lacks is asked for, not arranged around ([e2e-harness-guide.md](references/e2e-harness-guide.md#verifying-against-a-real-server-env-targets)).
3. Choose the cheapest form that observes the contract, and put everything it produces under gitignored `e2e/scratch/<scenario>/`:

   | To reach and observe the target | You author |
   |---|---|
   | a headless command already reaches it | nothing — run it and read the oracle |
   | it needs the GUI or the IPC path | nothing — `make dev-sandbox`, then `drive.mjs` / `oracle.mjs` |
   | the sequence must be replayed, or timing/concurrency is the contract | a spec on the harness |

   This project:

   | Change lands in | Reach it with | You author | Oracle |
   |---|---|---|---|
   | asset operations — SSH, SQL, Redis, Mongo, file | `opsctl` against a throwaway `--data-dir` ([§6](references/testing-debugging-guide.md#6-headless-functional-testing-with-opsctl)) | nothing | `audit_logs` and `logs/` ([§3](references/testing-debugging-guide.md#3-reading-logs), [§4](references/testing-debugging-guide.md#4-inspecting-the-database)) |
   | UI rendering — layout, copy, disabled state | `make dev-sandbox`, then `drive.mjs snapshot` / `shot` | nothing | the snapshot and the screenshot |
   | GUI or IPC that touches data | `make dev-sandbox`, then `drive.mjs` to act | nothing | `oracle.mjs audit` / `assets` / `logs` |
   | the packaged native shell — window, tray, updater, installer | run the built binary by hand | nothing | `logs/` |
   | timing or concurrency — batch exec, keep-alive, timeouts | the harness | a spec | the spec's assertions |

   The first three rows author nothing because the sandbox holds the app open; the last row needs a spec because asserting *when* something happened is not something you can do by hand.

   In every form one observation comes from a path the driven surface does not share — an `audit_logs` row, a structured log line, or a read-only query. `oracle.mjs mark` before you start, `oracle.mjs audit --since=<that id>` after, so you read back only what your flow produced.

4. Before running, create `report.md` from [references/verification-report-template.md](references/verification-report-template.md); update it as evidence arrives.
5. Record how the target was driven, exit codes where the form produces them, deciding runtime observations, gaps and shortest user reproduction steps.

For acceptance against a spec, `<scenario>` is the spec slug — pass it as `--scenario=<slug>` (or `OPSKAT_SCENARIO=<slug>`) so screenshots land beside the report. Extract each requirement into one verdict row and evidence section. Verdict labels are `holds`, `does not hold`, `not observed`.

For bug reproduction, state whether the reproduction asserts expected behaviour (red until fixed) or current buggy behaviour (green until fixed), then turn it into the committed failing test the [fix policy](../AGENTS.md#fix-policy--tdd-root-cause-in-scope) requires. Choosing a form that authors nothing does not remove that test.

Never weaken an assertion, skip a failed step or describe red as green. For background and runtime effects, use a specific `audit_logs` row or structured log line; "no errors" is not evidence. Obtain authorization before destructive or external side effects — a real `.env` target sits outside every isolation guarantee, so keep operations read-only and clean up the seeded asset — and before substituting a mock for a real dependency. The in-harness `redis-mock`, `ssh-mock` and `openai-mock` are substitutions: the verdict row names which one stood in and what it does not cover, such as `ssh-mock` accepting any client because it runs `NoClientAuth`.

## Maintaining this route

Harness facts are owned by [e2e-harness-guide.md](references/e2e-harness-guide.md). Follow [DOC-MAINTENANCE.md](DOC-MAINTENANCE.md) after path or harness changes. What this route still owns:

```bash
grep -n "e2e/scratch" .gitignore                        # evidence stays local
grep -n -A2 '^dev-sandbox:' Makefile                    # the launch command still exists
grep -n "OPSKAT_E2E=1 标记" main.go                      # the data-isolation gate is still enforced
go test -run TestResolveBootstrapRefusesProductionDataDir .   # …and still proven
```
