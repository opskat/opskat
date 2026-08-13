# E2E Harness Guide (Playwright × the real Wails app)

> How to drive the **real running OpsKat app** end-to-end with Playwright — both the
> committed core-flow suite and **ad-hoc functional verification of a feature you just
> finished**. Written for agents (Claude / Codex) and developers.
>
> This doc **owns** the GUI-e2e harness. For logs / DB / `opsctl` headless verification see
> [testing-debugging-guide.md](./testing-debugging-guide.md).

OpsKat is an IPC-only Wails desktop app — there is no HTTP API to hit. But `wails dev`
exposes the app over a browser-accessible IPC bridge, so Playwright (Chromium) can open it
like a normal page and drive the **real frontend → real Wails IPC → real Go service/
repository → real SQLite**. That makes it the one harness that exercises the whole stack the
way a user does.

## 1. Two modes — pick the right one

| | **Committed core-flow suite** | **Ad-hoc functional verification** |
|---|---|---|
| Lives in | `e2e/tests/*.spec.ts` (committed) | nothing — you drive the live sandbox |
| Run with | `make test-e2e` | `make dev-sandbox`, then `drive.mjs` / `oracle.mjs` |
| Lifetime | permanent regression guard | the session you are in |
| What goes here | **only core / critical flows** | "I just built X — does it actually work in the real app?" |
| Audience | everyone, every time the suite runs | you / the AI, right now |

**The bar for a committed spec is high.** A committed GUI e2e spec is slow (builds + runs
the real app, minutes per run) and is a maintenance liability. Only add one for a **core
flow** — app boots, primary navigation, create/connect the main asset types, a critical
data-integrity path. Everything else gets **verified ad-hoc** (mode 2). When in doubt,
verify ad-hoc; promote to a committed spec only once the flow is clearly core and stable.

**Most feature verification is mode 2, and mode 2 authors nothing.** The sandbox holds the
real app open on an isolated data dir; you look at it, act on it, and read its side-effects
one command at a time, changing your mind as you go. A throwaway spec is warranted only
when a sequence must be replayed identically or when timing/concurrency is the contract.
See §6.

## 2. Architecture

```
make test-e2e  →  cd e2e && pnpm test  →  node run-e2e.mjs   (spawns playwright, cleans up after)
  └─ playwright (workers:1)
       ├─ webServer:  wails dev -devserver localhost:<app>    (real Go app, native window opens)
       │                 ├─ vite (frontend HMR)
       │                 └─ opskat app  → service/repository → <tmp>/opskat-e2e-data-<ws>/opskat.db
       └─ chromium → http://localhost:<app>    (Wails IPC websocket bridge → real Go backend)
                         └─ specs assert on the UI …
                              … and on the DB via a direct read-only node:sqlite query (independent oracle)
```

The app is launched with these env overrides (injected by `e2e/playwright.config.ts`; read
in `main.go` via `resolveBootstrap()` and `initExtensionSystem`):

| Env | Effect |
|---|---|
| `OPSKAT_DATA_DIR=<tmp>/opskat-e2e-data-<workspaceId>` | DB, config, sockets, logs all under a throwaway dir |
| `OPSKAT_MASTER_KEY=<fixed test key>` | passphrase for credential KDF; **bypasses the OS keychain** (`ResolveMasterKey` returns the explicit key) |
| `OPSKAT_E2E=1` | disables the single-instance lock so the e2e app coexists with a running opskat |
| `OPSKAT_EXTENSIONS=0` | skips the slow WASM extension init |

The bridge runs on a **dedicated port** — never Wails' default 34115 — so it never reuses, or
collides with, a dev server you (or the sibling `agentre` app) already have open. The exact
number is allocated per checkout (§3, "Workspace"): the first checkout gets 34216, the next
34224, and so on. `node e2e/oracle.mjs where` prints what yours resolved to.

## 3. Isolation & safety guarantees

A run is fully hermetic, and in particular **a running opskat does not interfere**:

- **Data** — DB / config / `master.key` live under `<tmp>/opskat-e2e-data-<workspaceId>`, removed by
  `run-e2e.mjs` after the run. Your real `~/Library/Application Support/opskat` is never touched.
- **Keychain** — the explicit `OPSKAT_MASTER_KEY` short-circuits keychain access; nothing is
  read from or written to the OS keychain.
- **Sockets** — `approval.sock` / `sshpool.sock` are built from `bootstrap.ResolvedDataDir()`
  (the resolved override), so they land in the temp dir. A real opskat holds its own sockets
  in the real dir; no `another instance is already listening` collision.
- **Single-instance lock** — `OPSKAT_E2E=1` skips it, so the e2e instance launches even with
  a real opskat open, and doesn't trigger the real app's second-instance handler.
- **Port** — never Wails' default 34115. The committed `boot` spec also asserts the page
  `<title>` is `OpsKat`, so if some *other* app ever answered on the port the suite fails
  loudly instead of false-greening. The interactive sandbox (§6) gets its *own* port rather
  than reusing the suite's: the suite's webServer entries run with `reuseExistingServer`
  locally, so sharing would let `make test-e2e` silently adopt a live sandbox and report green
  against an app it never built.
- **Workspace** — every isolated resource above is keyed by `workspaceId`, an 8-hex hash of
  the checkout path (`harness/env.js`). Ports come in blocks of 8 from 34216 (`+0` suite app,
  `+1` redis, `+2` ssh, `+3` openai, `+4` sandbox app, `+5` sandbox CDP); the block index is
  claimed once per checkout and recorded in `~/.opskat-verify/workspaces.json`, under an
  exclusive-create lock. It is persisted rather than recomputed because Playwright
  re-evaluates the config in every worker and `drive.mjs` runs as a fresh process per command
   — all of them must resolve the same block. Data dirs, log files and the session file carry
  the same id, and `reapOrphanVite` matches only this checkout's `frontend/` path, so **several
  worktrees can verify at the same time** without colliding or reaping each other.
- **Enforced, not just arranged** — every bullet above is something the harness *sets up*. The
  one that must not depend on setup being correct is the data dir, so it is also checked in the
  app: `resolveBootstrap` in `main.go` returns an error, and `main` exits, when `OPSKAT_E2E=1`
  is set and the resolved data dir is (or resolves to, via `portable.SameDir` — trailing slash,
  `.`, symlink, Windows case) the real `AppDataDir()`. Proven by
  `TestResolveBootstrapRefusesProductionDataDir`. This is what makes "verification never touches
  your real inventory" a property of the app rather than a convention in a document.

The one thing that *does* matter: extra running apps add machine load and slow the `wails
dev` build. Correctness-wise nothing is shared any more — a sandbox and a suite run, or two
worktrees, can overlap freely.

## 4. Running the committed suite

```bash
cd e2e && pnpm run setup   # one-time: install deps + Chromium (skip if already done / on CI)
make test-e2e              # or, equivalently: cd e2e && pnpm test
```

Prereqs: `wails` CLI on PATH, `pnpm`, Node (with the built-in `node:sqlite` — Node ≥ 22).
`pnpm run setup` installs the e2e deps and the Chromium build **once**; `make test-e2e`
itself only runs the suite — no per-run install. First run builds Go + Vite (a few minutes)
and **opens a native OpsKat window** — expected; the test drives the headless browser
instance, not that window. The window closes when the suite ends.

**Platforms.** Runs on macOS, Linux, and native Windows. `make test-e2e` is a thin alias for
`cd e2e && pnpm test`, so on Windows (no `make`) run `cd e2e && pnpm test` directly. *All*
orchestration and cleanup live in `e2e/run-e2e.mjs` (cross-platform Node) — there are no
shell-only `pkill`/`mkdir -p`/`touch` steps. CI exercises the Linux path.

The suite (`e2e/tests/`): `boot` (app mounts + `OpsKat` title), `smoke` (layout + sidebar
nav), `asset-crud` (create an SSH asset via the form → tree → `node:sqlite` read of the temp
DB), `asset-lifecycle` (edit-renames + delete-soft-deletes an asset via the right-click
context menu, each verified in the tree **and** on disk), `redis-connect` (create a
*Redis* asset, drive its **Test Connection** against an in-harness mock Redis so the app
actually dials and `PING`s, then persist), and `ssh-connect` (create an *SSH* asset and drive
**Test Connection** against an in-harness mock SSH server — the app completes a real SSH
handshake — then persist), plus the **AI exec** specs — `ai-exec` (the unified `exec` tool:
approval dialog → real execution → audit row), `ai-exec-crud` (asset CRUD through AI tools),
`ai-exec-gate` (nothing unexecutable reaches the approval dialog), and `ai-exec-policy`
(policy allow / deny and grant persistence), all driven by a **scripted model** (§4.1).
After Playwright exits, `run-e2e.mjs` reaps the orphan `vite` and removes the temp data dir
(see §7). It also removes `<tmpdir>/opskat-e2e-webserver.log` after a successful run but
preserves that log after a failure.

The mock Redis (`e2e/fixtures/redis-mock.mjs`, pure Node, no deps) is started as a **second
Playwright `webServer`** on its own port from this checkout's block (`+0` is the app, `+1` the
mock); the spec
reads the port from `process.env.MOCK_REDIS_PORT`. It answers only what go-redis v9's connect
handshake needs — `HELLO` → a `-ERR` reply (triggers the RESP2 fallback), `PING` → `+PONG`,
everything else → `+OK` — which is why no real Redis is needed. This is the reusable shape for
**any "the asset really connects" spec**: stand up a tiny protocol mock as a second
`webServer` (TCP `port` readiness) and point the asset at `127.0.0.1:<port>`. `ssh-connect`
uses the same shape with a Go mock — `e2e/fixtures/ssh-mock/main.go`, a tiny `x/crypto/ssh`
server (`NoClientAuth`, so the connect "none" probe succeeds; random host key auto-trusted on
first connect) on `+2`, `go run` as a third `webServer`. That SSH mock also **echoes back
any command it is asked to `exec`** (`mock-exec-ran: <command>`), which is what lets the AI
specs compare the string the *server* received against the one the approval dialog showed.

### 4.1 Driving the AI stack with a scripted model

The AI specs run the **real** AI stack — `internal/ai/runner` → cago-agents `provider/openai` →
`sashabaranov/go-openai` — against `e2e/fixtures/openai-mock.mjs` (pure Node) on `+3`,
a fourth `webServer`. It speaks just enough OpenAI: SSE `/v1/chat/completions` plus a
control API, so a **spec** decides what the "model" does — the mock hard-codes nothing:

```ts
await scriptModel([                                        // fixtures/ai.ts
  { tool: { name: "help", args: { asset } } },             // one tool call per model round
  { tool: { name: "exec", args: { asset, command: "uptime" } } },
  { text: "done" },                                        // …then a plain reply ends the turn
]);
await openNewChat(page);                                   // fresh conversation ⇒ fresh doc gate
await sendChat(page, `run uptime on ${asset}`);
```

`mockRequests()` / `toolResultsSeenByModel()` read back what the app *sent* the model, which
is how a spec asserts "the model was told the user denied it".

The provider row itself is created through the **real** `CreateAIProvider` /
`SetActiveAIProvider` bindings (`ensureAIProvider`), not seeded as SQL: `api_key` is
encrypted at rest by `credential_svc`, so a plaintext INSERT would fail to decrypt on
activate. Assets, by contrast, are seeded directly (§8) — SSH against the `NoClientAuth`
mock and Redis with no password need no credential row.

Two behaviors bite when writing these specs, both by design in the backend:

- **`exec` is doc-gated.** The first `exec` against a given asset *type* in a conversation
  is refused with guidance text until the model has called `help(asset)` in that same
  conversation — so a script that jumps straight to `exec` is testing the gate, not exec.
- **Types have default policies.** SSH/etcd/Redis assets with no `command_policy` inherit
  built-in read-only allow lists, so a *read* command is auto-allowed with no dialog. Use a
  write command (`SET …`, `put …`) when the spec needs the approval prompt.

**In CI:** the committed suite runs on every PR / push as the `Wails E2E` job (`ubuntu-22.04`)
in `.github/workflows/ci.yml` — it installs `xvfb` + GTK/WebKit, then runs `xvfb-run -a make
test-e2e`; on failure it uploads `e2e/playwright-report`, `e2e/test-results`, and the webServer
log as build artifacts. The ad-hoc scratch mode (`make test-e2e-scratch`) is local-only.

## 5. Writing a committed core-flow spec

Only when the flow is genuinely core (§1). Conventions:

- **Locators: `data-testid`.** Add a stable `data-testid` to the element you assert on
  (additive only — never change markup/behavior to test it). Existing ids: `app-root`,
  `nav-<page>` / `nav-settings` (+ `data-active`), `asset-tree`, `add-asset-button`,
  `asset-form-dialog`, `asset-form-name-input`, `asset-form-submit`, `ssh-host-input`,
  `asset-type-picker` / `asset-type-option-<type>`, `redis-host-input` / `redis-port-input`,
  `asset-test-connection`, the asset right-click items `asset-context-edit` /
  `asset-context-delete`, and the delete confirm button `confirm-delete-asset` (a
  `confirmTestId` prop threaded through the shared `ConfirmDialog`). AI side: `ai-new-chat`,
  `ai-chat-input`, `ai-send-button` / `ai-stop-button`, `ai-tool-block` (+ `data-tool-name` /
  `data-status`) and `ai-tool-output`, and the approval block `ai-approval-block`
  (+ `data-approval-kind`) with `ai-approval-command` / `ai-approval-deny` /
  `ai-approval-remember` / `ai-approval-allow-all` / `ai-approval-allow`. Reuse these; add new
  ones in the same style.
- **No `sleep`.** Use Playwright's auto-waiting assertions (`await expect(locator).toBeVisible()`,
  `.toBeHidden()`, `expect.poll(...)`). Sleeps are the #1 source of flake.
- **Verify side effects independently.** Asserting the UI updated is necessary but not
  sufficient — confirm the data really persisted with the DB oracle in `e2e/fixtures/db.ts`
  (a read-only `node:sqlite` query against the temp `opskat.db`). It's an oracle *independent*
  of the app's own service layer, so it catches "UI says OK but nothing was written" bugs.
  Add more `findXByY` helpers there as needed (read-only, `PRAGMA busy_timeout`).
- **Unique fixtures.** Name created entities uniquely (e.g. `e2e-ssh-${Date.now()}`) so reruns
  don't collide.

Shape of a spec (see `e2e/tests/asset-crud.spec.ts` for the full version):

```ts
import { test, expect } from "@playwright/test";
import { findAssetByName } from "../fixtures/db";

test("create SSH asset persists and shows in tree", async ({ page }) => {
  await page.goto("/");
  const name = `e2e-ssh-${Date.now()}`;
  await page.getByTestId("add-asset-button").click();
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("ssh-host-input").fill("example.com");
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-tree").getByText(name)).toBeVisible();   // UI
  await expect.poll(() => findAssetByName(name)?.status).toBe(1);                // DB oracle
});
```

## 6. Ad-hoc functional verification — the workflow after finishing a feature

This is the default way to answer **"I just built X — does it work end-to-end in the real
app?"** without committing a test. It is the GUI counterpart of the
[AGENTS.md "verify by observing, not asserting"](../../AGENTS.md#fix-policy--tdd-root-cause-in-scope)
rule: drive the real app, then read observable side-effects (UI, DB, logs).

1. **Start the sandbox.** It returns immediately and is idempotent — a second call reports the
   running session rather than starting a rival app.
   ```bash
   make dev-sandbox            # ARGS=--reset wipes it first; ARGS=--mocks adds the protocol mocks
   make dev-sandbox-status     # what's up for this checkout
   make dev-sandbox-down       # stop it (the data dir survives)
   ```
   It boots the real app on this checkout's own sandbox dir (`~/.opskat-verify/<workspaceId>`,
   persistent across launches) with a **headless** Chromium attached over CDP, and prints the
   port, data dir, DB path and log paths — run `node e2e/oracle.mjs where` any time to see them
   again. Isolation is the same as §3 — plus a hard gate: `main.go`'s `resolveBootstrap`
   **refuses to boot** any `OPSKAT_E2E=1` run whose data dir resolves to the real one, so this
   cannot be pointed at your live inventory. `ARGS=--headed` shows the browser when you want to
   watch it work.
2. **Look, act, observe — one command at a time.** Each `drive.mjs` invocation attaches over
   CDP, does one thing and exits; the page and its state persist in between.
   ```bash
   node e2e/drive.mjs snapshot                    # visible structure: testids, roles, text, state
   node e2e/drive.mjs click add-asset-button      # a bare word is a data-testid (§5)
   node e2e/drive.mjs fill ssh-host-input example.com
   node e2e/oracle.mjs audit --since=41           # the independent DB oracle, read live
   ```
   `snapshot` is what removes the selector guesswork: it lists what is actually on screen with
   the testid to address it by, plus roles, values and disabled state — including inside modals,
   which live in a portal. A bare word is a testid; otherwise name the kind (`text=`, `role=`,
   `label=`, `placeholder=`, `title=`, `css=`). If the feature needs a UI hook that doesn't exist
   yet, add a `data-testid` (additive); if it surfaces a real bug, fix the producer per the Fix
   policy.
3. **Record while you go.** Every `drive.mjs` call appends one line — command, arguments and
   outcome, including failures and refusals — to `e2e/scratch/<slug>/drive.log`, and
   `shot <name>` writes the screenshot beside it. `oracle.mjs` output is copied into the same
   directory. The sandbox's data dir persists after `down`, so unlike a suite run there is no
   race to capture evidence before teardown. (Log/DB reading mechanics:
   [testing-debugging-guide.md](./testing-debugging-guide.md).)
4. **When a spec really is needed** — a sequence that must be replayed identically, or timing /
   concurrency as the contract — write it under `e2e/scratch/<scenario>/` (gitignored) with the
   same conventions as §5 and run it on the hermetic harness:
   ```bash
   cd e2e && pnpm run test:scratch scratch/<scenario>/verify.spec.ts
   make test-e2e-scratch        # everything left under e2e/scratch/
   ```
   `playwright.scratch.config.ts` reuses the exact same webServer / env / isolation as the
   committed suite — only `testDir` points at `./scratch`. That is a *different* run from the
   sandbox: the suite's own port, and a temp data dir the runner **deletes when Playwright exits**, so such a
   spec must capture its evidence during the run. On failure, inspect the preserved
   `<tmpdir>/opskat-e2e-webserver.log` and the trace/screenshots under `e2e/test-results/`.
5. **Discard.** Anything under `e2e/scratch/` is gitignored. If the flow turns out to be core
   and worth guarding forever, *promote* it: move it into `e2e/tests/`, harden it, and commit (§5).

**Keeping durable artifacts for review.** One scenario, one gitignored directory
`e2e/scratch/<scenario>/`, holding `report.md`, screenshots and oracle output — the layout and
verdicts are owned by
[verification-report-template.md](./verification-report-template.md). Whatever produced the
evidence stays throwaway; only genuinely core flows get promoted and committed (§5).

See [`e2e/scratch/README.md`](../../e2e/scratch/README.md) for the copy-paste starter, and
`node e2e/drive.mjs --help` / `node e2e/oracle.mjs --help` for the full command sets.

### Verifying against a real server (`.env` targets)

Everything above is **hermetic** — specs point assets at the in-harness mocks (`redis-mock`
/ `ssh-mock`, §4), so a run touches no real infra. That's the right default. But some checks
only a **real** server can answer: a real SSH handshake and interactive shell, real
SFTP/filesystem behavior, a protocol quirk the mock doesn't fake. For those, keep a
gitignored **`.env`** at the repo root listing real verification targets, with
**`.env.example`** as the committed template (one `# --- <type> ---` block per real target
currently covered by the template: SSH / MySQL / PostgreSQL / Redis / MongoDB / etcd / OSS /
RDP / VNC).

`.env` is **read by no app code** — the app never loads it. The e2e harness
(`playwright.config.ts`) loads it into `process.env` when present, so a spec / your tooling reads
the target's host / port / user / key straight from the environment and wires it into whichever
verification path fits. Clean up the seeded asset after.

- **Scratch spec (real SSH).** Seed a key-auth SSH asset pointing at the `.env` target, using
  the §8 `node:sqlite` seed pattern. Key auth reads the private-key file straight from disk at
  connect (`credential_resolver` → `os.ReadFile(private_keys[0])`), so a key-auth asset needs
  **no** credential row and **no** `OPSKAT_MASTER_KEY` — the config JSON alone is enough. That
  same `os.ReadFile` does **not** expand `~`, so store an **absolute** path (expand `~`
  yourself):
  ```ts
  const key = process.env.E2E_SSH_KEY!.replace(/^~(?=\/)/, process.env.HOME!); // config needs an ABSOLUTE path (connect's os.ReadFile won't expand ~)
  const cfg = JSON.stringify({
    host: process.env.E2E_SSH_HOST, port: Number(process.env.E2E_SSH_PORT ?? 22),
    username: process.env.E2E_SSH_USER, auth_type: "key", private_keys: [key], // asset_entity.SSHConfig json tags
  });
  db.prepare("INSERT INTO assets (name, type, group_id, config, status, ssh_tunnel_id, extension_name) VALUES (?,?,?,?,?,?,?)")
    .run("e2e-ssh-real", "ssh", 0, cfg, 1, 0, "");                             // group_id 0 → tree root
  ```
  `playwright.config.ts` loads the repo-root `.env` into `process.env` (optional — skipped when
  the file is absent, e.g. on CI; an already-set env var wins), so a spec reads
  `process.env.E2E_SSH_*` exactly like `OPSKAT_DATA_DIR`. Keep one `KEY=value` per line (no inline
  comments) so the loader parse stays trivial. This zero-credential seed is **specific to
  key-auth SSH** — password-auth types (`database` / `redis` / `mongodb` / `oss` / `rdp`) store
  their secret **AES-encrypted** (`credential_svc`), so a plaintext password in raw seeded config
  JSON won't decrypt; create those via `opsctl` or the create-asset form (below), which encrypt
  through the service layer.
- **Headless (`opsctl`).** Or drive the real target through `opsctl` and read `audit_logs` —
  see [testing-debugging-guide.md §6](./testing-debugging-guide.md#6-headless-functional-testing-with-opsctl).
  Prefer an isolated `--data-dir` so the seeded asset never lands in your real inventory.

**Not hermetic — real side effects.** A real target sits outside every isolation guarantee in
§3. Keep ops **read-only / nondestructive**, never point a destructive scratch spec at it, and
don't commit the seeded asset or the `.env`.

## 7. Harness engineering — hard-won lessons (symptom → root cause → fix)

These bit us while building the harness; keep them in mind when changing it.

- **False green against the wrong app.** *Symptom:* suite "passes" but never built opskat.
  *Cause:* a dev server (opskat or the `agentre` fork) on Wails' default 34115 +
  `reuseExistingServer` reusing it. *Fix:* a dedicated per-checkout port + the `boot` spec asserts
  the `OpsKat` title.
- **`unable to open database file` in the DB oracle.** *Symptom:* UI passed, oracle threw.
  *Cause:* Playwright re-evaluates `playwright.config.ts` in **every worker** process, so a
  module-top-level `mkdtemp` produced a *different* dir per process. *Fix:* a **deterministic**
  fixed dir (`join(tmpdir(),"opskat-e2e-data")`), cleaned/created **only in the main runner**
  (`if (process.env.TEST_WORKER_INDEX === undefined)`) before the webServer launches; workers
  reuse the same path.
- **Suite hangs forever after tests pass.** *Symptom:* all green, but `pnpm test` never exits.
  *Cause:* `wails dev` orphans its `vite` child, which keeps the **piped** webServer stdout's
  write end open, so the Node runner's readable stream never ends. *Fix:* `stdout/stderr:
  "ignore"` + redirect the command's own output to a file (`wails dev ... > "$LOG" 2>&1`);
  readiness is detected via `url` polling, not stdout.
- **All green but `exit 143` / `make: *** Terminated`.** *Symptom:* tests pass, the run still
  reports failure (SIGTERM). *Cause:* reaping inside `globalTeardown` SIGTERMs Playwright's
  *still-managed* webServer (it tears down **after** globalTeardown); reaping via a Makefile
  `pkill` instead self-matches the recipe shell's own command line on Linux (procps reads
  `/proc/<pid>/cmdline`) and SIGTERMs `make`. *Fix:* do **all** post-run cleanup in
  `e2e/run-e2e.mjs` — it spawns `playwright test`, and *after* Playwright has torn the
  webServer down (app gone, db closed, `vite` orphaned) it reaps the orphan `vite` (scoped to
  this repo's frontend so it never touches `agentre`) and removes the temp dir, then exits
  with Playwright's code. No `pkill` / `globalTeardown`, so it's cross-platform and a bare
  `pnpm test` behaves exactly like `make test-e2e`.
- **Collision with a running opskat.** *Symptom:* `another instance is already listening on
  …/approval.sock`. *Cause:* socket paths built from `AppDataDir()` ignored the override.
  *Fix:* `internal/app/opsctl/approval.go` uses `bootstrap.ResolvedDataDir()` (§8).
- **Single-instance lock blocks the e2e app.** *Fix:* `OPSKAT_E2E=1` skips
  `SingleInstanceLock` (see `main.go`).
- **Click and screenshot hang; `evaluate` works.** *Symptom:* the element is visible and enabled
  in `snapshot`, but `drive.mjs click` times out, and so does `shot`. *Cause:* the sandbox's
  Chromium was spawned as a bare binary, and a headless Chromium launched that way never
  produces frames — `requestAnimationFrame` never fires, so every Playwright action that waits
  for an element to be *stable* waits forever, while `evaluate` (which needs no frame) keeps
  working. Copying Playwright's launch flags by hand did **not** fix it. *Fix:* let Playwright
  launch the browser, and keep it alive in a detached owner process
  (`e2e/harness/browser-host.mjs`) so it outlives the short-lived `sandbox.mjs up`.
- **Teardown leaves the app and vite running.** *Symptom:* `down` reports success, but the port
  is still held. *Cause:* the app is spawned through a `sh -c` wrapper, so killing that pid
  orphans `wails` and its children. *Fix:* spawn `detached` (own process group) and signal the
  **group** (`process.kill(-pid)`), then sweep for orphan vite by command line.
- **Master key format.** Any non-empty string works — it's an Argon2id passphrase
  (`credential_svc.New`), not a fixed-length key, so a literal test string is fine.

## 8. Extending the harness

- **New env-overridable boot input** → thread it through `bootstrap.Options` and read it in
  `main.go:resolveBootstrap()` (mirrors `OPSKAT_DATA_DIR` / `OPSKAT_MASTER_KEY`). Don't invent
  a parallel config path.
- **A new path derived from the data dir** (a file, a socket, a subdir created at startup) →
  build it from `bootstrap.ResolvedDataDir()`, **not** `AppDataDir()`, or it won't follow the
  e2e override and will break hermeticity / collide with a running app. (`GetLogsDir()` and the
  approval/sshpool sockets already do this; other on-demand readers like the Settings page's
  data-dir display still use `AppDataDir()` — fix them to `ResolvedDataDir()` if a spec ever
  needs them.)
- **A new UI assertion target** → add a `data-testid` (additive) in the same style as §5.
- **A new persistence oracle** → add a read-only `node:sqlite` helper to `e2e/fixtures/db.ts`.
- **A spec that needs a pre-existing asset** (e.g. a database asset pointing at a local SQLite
  fixture, so you can drive the query panel / object browser without going through the
  create-asset form) → **seed the row directly** into the temp DB with a *writable* `node:sqlite`
  handle in `beforeAll`, before `page.goto`. The app reads assets from the DB on each fetch, so it
  shows up on mount — no restart. A SQLite asset needs no credential (nothing to encrypt), so this
  needs no real server. Sketch:
  ```ts
  import { DatabaseSync } from "node:sqlite";
  // 1. build the fixture DB itself with node:sqlite (tables / views / triggers / indexes)
  new DatabaseSync("/abs/fixture.db").exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL); ...`);
  // 2. seed an asset that points at it (db.ts opens read-only; seeding is the writable counterpart)
  const db = new DatabaseSync(`${process.env.OPSKAT_DATA_DIR}/opskat.db`);
  const cfg = JSON.stringify({ driver: "sqlite", path: "/abs/fixture.db" }); // asset_entity.DatabaseConfig json tags
  db.prepare("INSERT INTO assets (name, type, group_id, config, status, ssh_tunnel_id, extension_name) VALUES (?,?,?,?,?,?,?)")
    .run("e2e-db", "database", 0, cfg, 1, 0, "");                            // group_id 0 → renders at the tree root
  db.close();
  ```
  Then locate the asset by name and **double-click** the row to open its query tab. (Seeding a
  non-SQLite asset also needs its credential row, which is encrypted with the harness's
  `OPSKAT_MASTER_KEY` — driving the create-asset form is simpler there.)
- **A spec that asserts localized text** → force the language before load with
  `page.addInitScript(() => localStorage.setItem("language", "zh-CN"))` (i18n init reads
  `localStorage.language`); otherwise the app follows the auto-detected default and your `是/否`
  vs `YES/NO` assertion is environment-dependent.
- **A spec where the app must really connect somewhere** → stand up a minimal protocol mock
  as an extra `webServer` entry (TCP `port` readiness, not `url`), point the asset at
  `127.0.0.1:<port>`, and assert via the form's **Test Connection** (`asset-test-connection`):
  its sonner toast carries `data-type="success"` / `"error"`, a locale-independent signal.
  Templates: `e2e/fixtures/redis-mock.mjs` for a line-protocol client (keep the mock to the few
  bytes the client's handshake needs, no full protocol); `e2e/fixtures/ssh-mock/main.go` when
  the client needs a real crypto handshake — reuse `x/crypto/ssh` with `NoClientAuth` rather
  than faking bytes.

## 9. File map

| Path | Role | Committed? |
|---|---|---|
| `e2e/harness/env.js` | **the shared harness facts**: ports, suite/sandbox data dirs, `.env` loading, `frontend/dist` prep, orphan-vite reaping, the mock list. One owner, so the suite and the sandbox cannot drift apart | yes |
| `e2e/sandbox.mjs` | the sandbox launcher (§6): `up` / `down` / `status`. `up` starts the real app on `~/.opskat-verify/<workspaceId>` at port `+4` plus a browser host, records the session, and returns | yes |
| `e2e/harness/browser-host.mjs` | the detached process that owns the sandbox's Chromium, so it outlives `up`. Playwright launches the browser (§7 — a hand-spawned headless Chromium never produces frames) | yes |
| `e2e/drive.mjs` | drives that sandbox one command at a time over CDP — `snapshot` / `click` / `fill` / `shot` / `eval` (§6) | yes |
| `e2e/oracle.mjs` | reads the live sandbox's DB and structured logs from the shell — `assets` / `audit` / `mark` / `sql` / `logs` | yes |
| `e2e/run-e2e.mjs` | cross-platform *suite* runner: spawns `playwright test`, then reaps orphan `vite` + removes temp dir after it exits | yes |
| `e2e/playwright.config.ts` | base harness: temp dir + env + `frontend/dist` prep, optional repo-root `.env` load (§6), four `webServer`s (mock Redis `+1`, mock SSH `+2`, mock OpenAI `+3`, + `wails dev` on `+0`) — all sourced from `harness/env.js` | yes |
| `e2e/playwright.scratch.config.ts` | extends base, `testDir: ./scratch` for throwaway specs | yes |
| `e2e/fixtures/db-queries.js` | the read-only `node:sqlite` statements, shared by specs and `oracle.mjs` | yes |
| `e2e/fixtures/db.ts` | the DB oracle as specs use it: typed views over `db-queries.js` + `waitForAuditLogs`'s Playwright polling | yes |
| `e2e/fixtures/ai.ts` | AI-spec plumbing: model scripting, provider setup via the real bindings, asset seeding, chat gestures, `execOutcome` (§4.1) | yes |
| `e2e/fixtures/redis-mock.mjs` | minimal pure-Node RESP mock (HELLO→`-ERR` / PING→`+PONG`), started as a 2nd webServer for the `redis-connect` spec | yes |
| `e2e/fixtures/ssh-mock/main.go` | minimal Go `x/crypto/ssh` server (`NoClientAuth`) that echoes `exec`'d commands, `go run` as a webServer | yes |
| `e2e/fixtures/openai-mock.mjs` | scripted OpenAI-compatible chat-completions server (SSE + `/__mock/*` control API) on `+3` (§4.1) | yes |
| `e2e/tests/*.spec.ts` | committed **core-flow** specs | yes |
| `e2e/scratch/<scenario>/` | one-off verification evidence: `report.md`, screenshots, any throwaway spec | **no (gitignored)** |
| `e2e/scratch/README.md` | scratch convention + starter template | yes |
| `e2e/package.json` → `setup` / `test` / `test:scratch` / `sandbox` / `drive` / `oracle` / `typecheck` | one-time install+Chromium / run suite / run scratch / the §6 tools / `tsc` | yes |
| `Makefile` → `dev-sandbox` / `test-e2e` / `test-e2e-scratch` | start the sandbox (`ARGS=…`) / thin aliases for `pnpm test` / `pnpm run test:scratch` | yes |
| `.env.example` | template for real-target verification (`.env` schema — one block per asset type), copied to a gitignored `.env` (§6) | yes |
| `.env` | real verification targets (host / port / user / credentials); read by no app code — `playwright.config.ts` loads it into `process.env` for §6 real-server checks | **no (gitignored)** |

Backend enablers that make it hermetic: `main.go` (`resolveBootstrap`, conditional
`SingleInstanceLock`), `internal/bootstrap` (`ResolvedDataDir`, `GetLogsDir`),
`internal/app/opsctl/approval.go` (socket paths).
