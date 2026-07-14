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
| Lives in | `e2e/tests/*.spec.ts` (committed) | `e2e/scratch/*.spec.ts` (**gitignored**) |
| Run with | `make test-e2e` | `make test-e2e-scratch` |
| Lifetime | permanent regression guard | throwaway — write, run, observe, delete |
| What goes here | **only core / critical flows** | "I just built X — does it actually work in the real app?" |
| Audience | everyone, every time the suite runs | you / the AI, right now |

**The bar for a committed spec is high.** A committed GUI e2e spec is slow (builds + runs
the real app, minutes per run) and is a maintenance liability. Only add one for a **core
flow** — app boots, primary navigation, create/connect the main asset types, a critical
data-integrity path. Everything else gets **verified ad-hoc** (mode 2) and the script
thrown away. When in doubt, verify ad-hoc; promote to a committed spec only once the flow
is clearly core and stable.

**Most feature verification is mode 2.** After finishing a feature, the right move is
usually: write a scratch spec, run it against the real app, read the assertions + DB + the
webServer log, confirm it works, then delete the script. See §6.

## 2. Architecture

```
make test-e2e  →  cd e2e && pnpm test  →  node run-e2e.mjs   (spawns playwright, cleans up after)
  └─ playwright (workers:1)
       ├─ webServer:  wails dev -devserver localhost:34216   (real Go app, native window opens)
       │                 ├─ vite (frontend HMR)
       │                 └─ opskat app  → service/repository → <tmp>/opskat-e2e-data/opskat.db
       └─ chromium → http://localhost:34216   (Wails IPC websocket bridge → real Go backend)
                         └─ specs assert on the UI …
                              … and on the DB via a direct read-only node:sqlite query (independent oracle)
```

The app is launched with these env overrides (injected by `e2e/playwright.config.ts`; read
in `main.go` via `resolveBootstrap()` and `initExtensionSystem`):

| Env | Effect |
|---|---|
| `OPSKAT_DATA_DIR=<tmp>/opskat-e2e-data` | DB, config, sockets, logs all under a throwaway dir |
| `OPSKAT_MASTER_KEY=<fixed test key>` | passphrase for credential KDF; **bypasses the OS keychain** (`ResolveMasterKey` returns the explicit key) |
| `OPSKAT_E2E=1` | disables the single-instance lock so the e2e app coexists with a running opskat |
| `OPSKAT_EXTENSIONS=0` | skips the slow WASM extension init |

The bridge runs on a **dedicated port 34216** (not Wails' default 34115) so it never reuses
— or collides with — a dev server you (or the sibling `agentre` app) already have open.

## 3. Isolation & safety guarantees

A run is fully hermetic, and in particular **a running opskat does not interfere**:

- **Data** — DB / config / `master.key` live under `<tmp>/opskat-e2e-data`, removed by
  `run-e2e.mjs` after the run. Your real `~/Library/Application Support/opskat` is never touched.
- **Keychain** — the explicit `OPSKAT_MASTER_KEY` short-circuits keychain access; nothing is
  read from or written to the OS keychain.
- **Sockets** — `approval.sock` / `sshpool.sock` are built from `bootstrap.ResolvedDataDir()`
  (the resolved override), so they land in the temp dir. A real opskat holds its own sockets
  in the real dir; no `another instance is already listening` collision.
- **Single-instance lock** — `OPSKAT_E2E=1` skips it, so the e2e instance launches even with
  a real opskat open, and doesn't trigger the real app's second-instance handler.
- **Port** — 34216, dedicated. The committed `boot` spec also asserts the page `<title>` is
  `OpsKat`, so if some *other* app ever answered on the port the suite fails loudly instead
  of false-greening.

The one thing that *does* matter: extra running apps add machine load and slow the `wails
dev` build. **Locally**, run one e2e invocation at a time (the temp data-dir path is fixed);
CI runners are isolated, so each job's run is independent.

## 4. Running the committed suite

```bash
cd e2e && pnpm run setup   # one-time: install deps + Chromium (skip if already done / on CI)
make test-e2e              # or, equivalently: cd e2e && pnpm test
```

Prereqs: `wails` CLI on PATH, `pnpm`, Node (with the built-in `node:sqlite` — Node ≥ 22).
`pnpm run setup` installs the e2e deps and the Chromium build **once**; `make test-e2e`
itself only runs the suite — no per-run install. First run builds Go + Vite (a few minutes)
and **opens a native OpsKat window** — expected; the test drives the `:34216` browser
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
handshake — then persist). After Playwright exits, `run-e2e.mjs` reaps the
orphan `vite` and removes the temp dir (see §7). webServer output →
`<tmpdir>/opskat-e2e-webserver.log`.

The mock Redis (`e2e/fixtures/redis-mock.mjs`, pure Node, no deps) is started as a **second
Playwright `webServer`** on a dedicated port (`34216` is the app; `34217` the mock); the spec
reads the port from `process.env.MOCK_REDIS_PORT`. It answers only what go-redis v9's connect
handshake needs — `HELLO` → a `-ERR` reply (triggers the RESP2 fallback), `PING` → `+PONG`,
everything else → `+OK` — which is why no real Redis is needed. This is the reusable shape for
**any "the asset really connects" spec**: stand up a tiny protocol mock as a second
`webServer` (TCP `port` readiness) and point the asset at `127.0.0.1:<port>`. `ssh-connect`
uses the same shape with a Go mock — `e2e/fixtures/ssh-mock/main.go`, a tiny `x/crypto/ssh`
server (`NoClientAuth`, so the connect "none" probe succeeds; random host key auto-trusted on
first connect) on `34218`, `go run` as a third `webServer`.

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
  `confirmTestId` prop threaded through the shared `ConfirmDialog`). Reuse these; add new ones
  in the same style.
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
[AGENTS.md "verify by observing, not asserting"](../AGENTS.md#fix-policy--tdd-root-cause-in-scope)
rule: drive the real app, then read observable side-effects (UI, DB, logs).

1. **Write a throwaway spec** under `e2e/scratch/` (gitignored). Same harness conventions as
   §5 — `data-testid` locators, auto-wait, the DB oracle. If the feature needs a UI hook that
   doesn't exist yet, add a `data-testid` (additive); if it surfaces a real bug, fix the
   producer per the Fix policy.
2. **Run it against the real app:**
   ```bash
   make test-e2e-scratch        # runs every e2e/scratch/*.spec.ts via the live harness
   # or a single file (still through the runner, so cleanup happens):
   cd e2e && pnpm run test:scratch scratch/<file>.spec.ts
   ```
   `playwright.scratch.config.ts` reuses the exact same webServer / env / isolation as the
   committed suite — only `testDir` points at `./scratch`.
3. **Observe.** Read the spec's assertions, then corroborate with the other surfaces:
   - DB — query the temp `opskat.db` with `findX` helpers (or add one), or open it read-only
     at `$OPSKAT_DATA_DIR/opskat.db`.
   - logs — the app's structured log is under `<tmp>/opskat-e2e-data/logs/`; the webServer's
     stdout/stderr is at `<tmpdir>/opskat-e2e-webserver.log`. (Log/DB reading: see
     [testing-debugging-guide.md](./testing-debugging-guide.md).)
   - on failure, Playwright keeps a trace/screenshot under `e2e/test-results/`.
4. **Discard.** The scratch file is gitignored — delete it (or leave it; it's never
   committed). If the flow turns out to be core and worth guarding forever, *promote* it: move
   it into `e2e/tests/`, harden it, and commit (§5).

**Keeping durable artifacts for review.** If a verification produces screenshots / reports you
want to hand off or look at later (not just observe-then-delete), save them under
`docs/verification/<topic>/` — that path is **gitignored** (like `e2e/scratch/`), so the
artifacts live locally without polluting the PR. The scratch spec that produced them stays
throwaway; only genuinely core flows get promoted and committed (§5).

See [`e2e/scratch/README.md`](../e2e/scratch/README.md) for a copy-paste starter.

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
  `reuseExistingServer` reusing it. *Fix:* dedicated port **34216** + the `boot` spec asserts
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
| `e2e/run-e2e.mjs` | cross-platform runner: spawns `playwright test`, then reaps orphan `vite` + removes temp dir after it exits | yes |
| `e2e/playwright.config.ts` | base harness: temp dir + env + `frontend/dist` prep, optional repo-root `.env` load (§6), three `webServer`s (mock Redis on `34217`, mock SSH on `34218`, + `wails dev -devserver 34216`) | yes |
| `e2e/playwright.scratch.config.ts` | extends base, `testDir: ./scratch` for throwaway specs | yes |
| `e2e/fixtures/db.ts` | read-only `node:sqlite` DB oracle (`findAssetByName`, …) | yes |
| `e2e/fixtures/redis-mock.mjs` | minimal pure-Node RESP mock (HELLO→`-ERR` / PING→`+PONG`), started as a 2nd webServer for the `redis-connect` spec | yes |
| `e2e/fixtures/ssh-mock/main.go` | minimal Go `x/crypto/ssh` server (`NoClientAuth`), `go run` as a webServer for the `ssh-connect` spec | yes |
| `e2e/tests/*.spec.ts` | committed **core-flow** specs | yes |
| `e2e/scratch/*.spec.ts` | throwaway functional-verification specs | **no (gitignored)** |
| `e2e/scratch/README.md` | scratch convention + starter template | yes |
| `e2e/package.json` → `setup` / `test` / `test:scratch` | one-time install+Chromium / run suite / run scratch | yes |
| `Makefile` → `test-e2e` / `test-e2e-scratch` | thin aliases for `pnpm test` / `pnpm run test:scratch` | yes |
| `.env.example` | template for real-target verification (`.env` schema — one block per asset type), copied to a gitignored `.env` (§6) | yes |
| `.env` | real verification targets (host / port / user / credentials); read by no app code — `playwright.config.ts` loads it into `process.env` for §6 real-server checks | **no (gitignored)** |

Backend enablers that make it hermetic: `main.go` (`resolveBootstrap`, conditional
`SingleInstanceLock`), `internal/bootstrap` (`ResolvedDataDir`, `GetLogsDir`),
`internal/app/opsctl/approval.go` (socket paths).
