# Verification — confirming a change actually works

> Green unit tests prove the behaviors you asserted — **not** that the feature works. This doc owns the **order of verification** and the **ad-hoc verification workflow**; the mechanics live in the references — logs / DB / headless `opsctl` → [references/testing-debugging-guide.md](./references/testing-debugging-guide.md), the GUI e2e harness (committed suite + scratch mode) → [references/e2e-harness-guide.md](./references/e2e-harness-guide.md). It is the "how" behind [AGENTS.md → Verify by observing, not asserting](../AGENTS.md#fix-policy--tdd-root-cause-in-scope).

## 1. Cheap signals first

Lint + unit suites before ever driving the real app: `make lint`, `go test ./...`; frontend `pnpm lint`, `pnpm test` (full command list: [DEVELOP.md](./DEVELOP.md#common-commands)).

## 2. Then exercise the affected flow — three surfaces, prefer the top

| Way | When to use | Cost |
|-----|-------------|------|
| **Automated tests** (`go test`, `vitest`, e2e harness) | Logic in `service/`/`repository/`/`internal/ai/`, frontend stores/components | Fast, deterministic — always try first |
| **`opsctl` (headless CLI)** | Asset operations (SSH/SQL/Redis/Mongo/file/extension) that run through the *real* service layer | Medium — needs a real or fixture asset ([how-to](./references/testing-debugging-guide.md#6-headless-functional-testing-with-opsctl)) |
| **Drive the real app** (GUI e2e harness) | The IPC/GUI path itself, or behavior only reachable from the desktop app | Slow — scratch spec via `make test-e2e-scratch` ([harness](./references/e2e-harness-guide.md)) |

**Confirm what happened through observable side-effects**, not "no error": assert a specific structured-log line (`logs/opskat.log`) **and/or** a specific DB row (`opskat.db`, esp. `audit_logs`), then reset state — reading recipes in [testing-debugging-guide.md](./references/testing-debugging-guide.md).

## 3. Ad-hoc GUI verification ≠ growing the e2e suite

"I just built X — does it actually work in the real app?" → a **throwaway** scratch spec, not a committed one:

1. Write it under gitignored `e2e/scratch/` (reuse the harness conventions and the DB oracle).
2. `make test-e2e-scratch` — the same hermetic harness as the committed suite.
3. Observe: the spec's assertions + the DB oracle + the app / webServer logs.
4. **Discard.** Promote into `e2e/tests/` only when the flow is genuinely core and stable — a committed GUI spec costs minutes per run and is a maintenance liability.

Workflow details, isolation guarantees, and real-server (`.env`) targets: [e2e-harness-guide.md §6](./references/e2e-harness-guide.md).

## 4. Evidence, kept local — with a `report.md`

Playwright already leaves raw artifacts per run — `e2e/playwright-report/` (HTML, overwritten by the next run) and, on failure, traces / screenshots under `e2e/test-results/` — but those are transient. Anything worth handing off goes to the gitignored `docs/verification/<topic>/` dir:

- the screenshots / log excerpts / query output that prove the behavior, and
- a short **`report.md`**: what was verified (scenario + commit), how (the commands / scratch spec used), which evidence file shows what, and the conclusion.

Reference the report from the PR or conversation — evidence before assertions. Nothing under `docs/verification/` is committed; promoting the *spec* into the permanent suite stays a separate, deliberate decision (§3).

## 5. Reproduction is step one of a fix

A scratch repro **is** the "confirm the bug is real" step of [AGENTS.md → Fix policy](../AGENTS.md#fix-policy--tdd-root-cause-in-scope) — and it never replaces the failing **committed** test (`go test` / `vitest`) that must follow before touching the implementation.
