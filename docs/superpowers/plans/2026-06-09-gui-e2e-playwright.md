# GUI E2E (Playwright × Wails) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a local, hermetic GUI end-to-end test suite that drives the real running Wails app with Playwright — verifying smoke (app boots, main layout, sidebar navigation) and asset CRUD (create an SSH asset via the form → it appears in the asset tree → it is persisted to `opskat.db`, asserted by a direct SQLite query).

**Architecture:** Playwright launches `wails dev` (which exposes a browser-accessible IPC bridge at `http://localhost:34115`) with a temporary data dir + fixed test master key injected via env. Chromium drives the real frontend → real Wails IPC → real Go service/repository → temp SQLite. Persistence is verified by an **independent oracle**: a read-only `better-sqlite3` query against the temp `opskat.db`, not via the app's own service layer.

**Tech Stack:** Go 1.26 (main.go enabler + goconvey/testify test), Wails v2.12.0 dev bridge, Playwright Test (TypeScript), `better-sqlite3`, pnpm, Make.

**Spec:** `docs/superpowers/specs/2026-06-09-gui-e2e-playwright-design.md`

---

## File Structure

| Path | Responsibility |
|------|----------------|
| `main.go` (modify) | Extract `resolveBootstrap()` reading `OPSKAT_DATA_DIR`/`OPSKAT_MASTER_KEY`/`OPSKAT_E2E`; thread into `bootstrap.Options`; bypass `SingleInstanceLock` under `OPSKAT_E2E=1`. |
| `main_test.go` (create) | Unit test for `resolveBootstrap()` env handling. |
| `frontend/src/App.tsx` (modify) | `data-testid="app-root"` on the root layout div. |
| `frontend/src/components/layout/Sidebar.tsx` (modify) | `data-testid="nav-<id>"` + `data-active` on each nav button incl. settings. |
| `frontend/src/components/layout/AssetTree.tsx` (modify) | `data-testid="asset-tree"` on the tree container; `data-testid="add-asset-button"` on the add button. |
| `frontend/src/components/asset/AssetForm.tsx` (modify) | `data-testid` on dialog, name input, submit button. |
| `frontend/src/components/asset/SSHConfigSection.tsx` (modify) | `data-testid="ssh-host-input"` on the host input. |
| `e2e/package.json` (create) | Standalone (non-workspace) package: `@playwright/test`, `@types/node`. DB oracle uses built-in `node:sqlite` (Node 26). |
| `e2e/playwright.config.ts` (create) | Creates temp data dir + sets env at module top-level; `webServer` runs `wails dev` on a **dedicated devserver port (34216)** so it never collides with a default-port (34115) dev server of opskat/agentre; chromium project; `globalTeardown`. |
| `e2e/tsconfig.json` (create) | Minimal TS config so `@types/node` + `@playwright/test` types resolve in-editor (Playwright itself transpiles without type-checking). |
| `e2e/global-teardown.ts` (create) | Removes the temp data dir. |
| `e2e/fixtures/db.ts` (create) | `node:sqlite` read-only helper: `findAssetByName(name)` against `assets`. |
| `e2e/tests/boot.spec.ts` (create) | Pipeline smoke: app mounts at baseURL. |
| `e2e/tests/smoke.spec.ts` (create) | Main layout + sidebar navigation. |
| `e2e/tests/asset-crud.spec.ts` (create) | Create SSH asset via UI → assert tree + DB row. |
| `Makefile` (modify) | `test-e2e-gui` target. |
| `.gitignore` (modify) | Ignore `e2e/node_modules`, reports, results. |
| `docs/testing-debugging-guide.md` (modify) | New subsection documenting the GUI e2e flow. |

---

## Task 1: Backend enabler — env-injected data dir / master key + e2e single-instance bypass

**Files:**
- Modify: `main.go` (lines ~69-202)
- Test: `main_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `main_test.go`:

```go
package main

import (
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	. "github.com/smartystreets/goconvey/convey"
)

func TestResolveBootstrap(t *testing.T) {
	Convey("with e2e env overrides set", t, func() {
		t.Setenv("OPSKAT_DATA_DIR", "/tmp/opskat-e2e-xyz")
		t.Setenv("OPSKAT_MASTER_KEY", "test-master-key")
		t.Setenv("OPSKAT_E2E", "1")

		dataDir, opts, disableSingleInstance := resolveBootstrap()

		So(dataDir, ShouldEqual, "/tmp/opskat-e2e-xyz")
		So(opts.DataDir, ShouldEqual, "/tmp/opskat-e2e-xyz")
		So(opts.MasterKey, ShouldEqual, "test-master-key")
		So(disableSingleInstance, ShouldBeTrue)
	})

	Convey("with no env overrides", t, func() {
		t.Setenv("OPSKAT_DATA_DIR", "")
		t.Setenv("OPSKAT_MASTER_KEY", "")
		t.Setenv("OPSKAT_E2E", "")

		dataDir, opts, disableSingleInstance := resolveBootstrap()

		So(dataDir, ShouldEqual, bootstrap.AppDataDir())
		So(opts.DataDir, ShouldEqual, bootstrap.AppDataDir())
		So(opts.MasterKey, ShouldEqual, "")
		So(disableSingleInstance, ShouldBeFalse)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestResolveBootstrap -v`
Expected: FAIL to compile — `undefined: resolveBootstrap`.

- [ ] **Step 3: Add the `resolveBootstrap` helper**

Add to `main.go` (e.g. just above `func main()`):

```go
// resolveBootstrap reads optional env overrides used by the GUI e2e harness
// (and aligned with opsctl's --data-dir / OPSKAT_MASTER_KEY flags) and returns
// the resolved data dir, bootstrap options, and whether the single-instance
// lock must be disabled (so an e2e instance does not collide with a running app).
func resolveBootstrap() (dataDir string, opts bootstrap.Options, disableSingleInstance bool) {
	dataDir = bootstrap.AppDataDir()
	if env := os.Getenv("OPSKAT_DATA_DIR"); env != "" {
		dataDir = env
	}
	opts = bootstrap.Options{DataDir: dataDir, MasterKey: os.Getenv("OPSKAT_MASTER_KEY")}
	disableSingleInstance = os.Getenv("OPSKAT_E2E") == "1"
	return dataDir, opts, disableSingleInstance
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run TestResolveBootstrap -v`
Expected: PASS (2 convey blocks).

- [ ] **Step 5: Wire the helper into `main()`**

In `main.go`, replace the top of `main()`:

```go
	// 初始化数据库、凭证、Repository、迁移
	dataDir := bootstrap.AppDataDir()
	if err := bootstrap.Init(ctx, bootstrap.Options{}); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
```

with:

```go
	// 初始化数据库、凭证、Repository、迁移（e2e 可经 env 覆盖数据目录/master key）
	dataDir, bootstrapOpts, disableSingleInstance := resolveBootstrap()
	if err := bootstrap.Init(ctx, bootstrapOpts); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
```

- [ ] **Step 6: Use the resolved `dataDir` for the extension asset handler**

In `main.go`, the `AssetServer` handler currently calls `bootstrap.AppDataDir()` again. Replace:

```go
			Handler: opsctl.NewExtensionAssetHandler(filepath.Join(bootstrap.AppDataDir(), "extensions"), nil),
```

with:

```go
			Handler: opsctl.NewExtensionAssetHandler(filepath.Join(dataDir, "extensions"), nil),
```

- [ ] **Step 7: Make `SingleInstanceLock` conditional**

In `main.go`, change the `wails.Run(&options.App{...})` call so the options are built into a variable and the lock is set conditionally. Replace:

```go
	err = wails.Run(&options.App{
		Title:     "OpsKat",
```

with:

```go
	appOptions := &options.App{
		Title:     "OpsKat",
```

Then find the `SingleInstanceLock` field inside that literal:

```go
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.opskat.desktop",
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				sys.OnSecondInstanceLaunch()
			},
		},
```

Delete those lines from the literal, and after the literal's closing `}` insert:

```go
	}
	if !disableSingleInstance {
		appOptions.SingleInstanceLock = &options.SingleInstanceLock{
			UniqueId: "com.opskat.desktop",
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				sys.OnSecondInstanceLaunch()
			},
		}
	}

	err = wails.Run(appOptions)
```

(The literal previously ended with `})`; it now ends with `}` on its own line, followed by the conditional block and `err = wails.Run(appOptions)`.)

- [ ] **Step 8: Verify build + test + lint**

Run: `go build . && go test . -run TestResolveBootstrap && make lint`
Expected: build succeeds, test PASS, lint clean.

- [ ] **Step 9: Commit**

```bash
git add main.go main_test.go
git commit -m "✨ 桌面端支持 OPSKAT_DATA_DIR/MASTER_KEY/E2E 环境覆盖"
```

---

## Task 2: E2E harness scaffold + pipeline smoke

**Files:**
- Create: `e2e/package.json`, `e2e/playwright.config.ts`, `e2e/global-teardown.ts`, `e2e/tests/boot.spec.ts`
- Modify: `.gitignore`

- [ ] **Step 1: Create `e2e/package.json`**

```json
{
  "name": "opskat-e2e",
  "private": true,
  "version": "0.0.0",
  "description": "OpsKat GUI end-to-end tests (Playwright × Wails dev bridge)",
  "scripts": {
    "test": "playwright test"
  },
  "devDependencies": {
    "@playwright/test": "^1.49.0",
    "@types/node": "^22.0.0"
  }
}
```

> The DB oracle (Task 4) uses Node's built-in `node:sqlite` (stable on Node 26) — no native `better-sqlite3` dependency. `@types/node` is only for editor types; Playwright transpiles `.ts` specs without type-checking, so it never blocks a run.

- [ ] **Step 2: Create `e2e/playwright.config.ts`**

The temp data dir + env MUST be set at module top-level: Playwright starts `webServer` before `globalSetup`, and the config module is evaluated first. The webServer launches `wails dev` on a **dedicated devserver port (34216)** — NOT the default 34115 — because a developer (or a sibling project like agentre) may already have a dev server on 34115; reusing that would silently test the wrong app.

```ts
import { defineConfig, devices } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Created once when the config is loaded (before webServer starts). Workers and
// globalTeardown read OPSKAT_DATA_DIR from the inherited process env.
const dataDir = mkdtempSync(join(tmpdir(), "opskat-e2e-"));
const MASTER_KEY = "opskat-e2e-master-key-do-not-use-in-prod";
process.env.OPSKAT_DATA_DIR = dataDir;
process.env.OPSKAT_MASTER_KEY = MASTER_KEY;
process.env.OPSKAT_E2E = "1";
process.env.OPSKAT_EXTENSIONS = "0";

// Dedicated wails dev server port for e2e (avoids the default 34115).
const DEVSERVER = "localhost:34216";
const BASE_URL = `http://${DEVSERVER}`;

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"], ["html", { open: "never" }]],
  globalTeardown: "./global-teardown.ts",
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    // `wails dev -devserver` binds the IPC bridge to our dedicated port. The
    // mkdir/touch mirrors `make dev`'s prep for the //go:embed frontend/dist.
    command: `mkdir -p frontend/dist && touch frontend/dist/.keep && wails dev -devserver ${DEVSERVER}`,
    cwd: "..",
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 240_000,
    stdout: "pipe",
    stderr: "pipe",
    env: {
      OPSKAT_DATA_DIR: dataDir,
      OPSKAT_MASTER_KEY: MASTER_KEY,
      OPSKAT_E2E: "1",
      OPSKAT_EXTENSIONS: "0",
    },
  },
});
```

Also create `e2e/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "types": ["node"],
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": ["**/*.ts"]
}
```

- [ ] **Step 3: Create `e2e/global-teardown.ts`**

```ts
import { rmSync } from "node:fs";

export default function globalTeardown() {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (dataDir && dataDir.includes("opskat-e2e-")) {
    rmSync(dataDir, { recursive: true, force: true });
  }
}
```

- [ ] **Step 4: Create the pipeline smoke `e2e/tests/boot.spec.ts`**

`main.tsx` renders the app into `#root`; asserting `#root` has children proves the full Playwright → wails dev → Go bridge pipeline works, before any app-specific test-ids exist. The title assertion (`/OpsKat/`) guards against a *foreign* app squatting the port — if some other dev server answered, the title would differ and the test fails loudly instead of false-passing.

```ts
import { test, expect } from "@playwright/test";

test("app mounts via the wails dev bridge", async ({ page }) => {
  await page.goto("/");
  // Confirm we reached opskat (not another app on the port).
  await expect(page).toHaveTitle(/OpsKat/i);
  const root = page.locator("#root");
  await expect(root).toBeAttached();
  // React has rendered something into the root container.
  await expect(root.locator(":scope > *").first()).toBeVisible();
});
```

- [ ] **Step 5: Add e2e ignores to `.gitignore`**

Append to `.gitignore`:

```
# e2e (Playwright)
e2e/node_modules/
e2e/test-results/
e2e/playwright-report/
e2e/.last-run.json
```

- [ ] **Step 6: Install deps + browser, run the smoke**

Run:
```bash
cd e2e && pnpm install && pnpm exec playwright install chromium && pnpm test tests/boot.spec.ts
```
Expected: PASS. (First run is slow — `make dev` runs `pnpm install` + Vite + Go build; webServer timeout is 240s.)

> If `make dev` opens a native window, that is expected and harmless — the test drives the `:34115` browser instance, not the native webview.

- [ ] **Step 7: Commit**

```bash
git add e2e/package.json e2e/playwright.config.ts e2e/global-teardown.ts e2e/tests/boot.spec.ts e2e/pnpm-lock.yaml .gitignore
git commit -m "✅ 搭建 Playwright × Wails GUI e2e 骨架与启动冒烟"
```

---

## Task 3: Smoke spec — main layout + sidebar navigation

> Note on TDD: the test-ids below are plain DOM-attribute additions; a vitest asserting an attribute exists would be a tautology. The **Playwright spec in this task is the red test** — write it first, watch it fail for the missing test-ids, then add them to go green.

**Files:**
- Create: `e2e/tests/smoke.spec.ts`
- Modify: `frontend/src/App.tsx`, `frontend/src/components/layout/Sidebar.tsx`

- [ ] **Step 1: Write the failing smoke spec**

Create `e2e/tests/smoke.spec.ts`:

```ts
import { test, expect } from "@playwright/test";

const NAV_IDS = ["home", "forward", "sshkeys", "snippets", "audit", "settings"] as const;

test("main layout renders", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
});

test("sidebar navigates across all pages", async ({ page }) => {
  await page.goto("/");
  for (const id of NAV_IDS) {
    const btn = page.getByTestId(`nav-${id}`);
    await btn.click();
    await expect(btn).toHaveAttribute("data-active", "true");
    // App survives navigation to every page.
    await expect(page.getByTestId("app-root")).toBeVisible();
  }
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd e2e && pnpm test tests/smoke.spec.ts`
Expected: FAIL — `getByTestId("app-root")` / `nav-*` not found (timeout).

- [ ] **Step 3: Add `app-root` to the layout root**

In `frontend/src/App.tsx`, find the outermost layout div:

```tsx
      <div className="flex h-screen w-screen flex-col overflow-hidden bg-background">
```

Change to:

```tsx
      <div className="flex h-screen w-screen flex-col overflow-hidden bg-background" data-testid="app-root">
```

- [ ] **Step 4: Add `data-testid` + `data-active` to the array nav buttons**

In `frontend/src/components/layout/Sidebar.tsx`, find the nav button rendered per item (the `<button>` with `onClick={() => onPageChange(item.id)}` and `aria-label={item.label}`). Add the two attributes:

```tsx
        <button
          className={cn(
            "relative flex h-9 w-9 items-center justify-center rounded-lg transition-colors duration-150",
            activePage === item.id
              ? "bg-accent text-accent-foreground"
              : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
          )}
          onClick={() => onPageChange(item.id)}
          aria-label={item.label}
          data-testid={`nav-${item.id}`}
          data-active={activePage === item.id}
        >
```

- [ ] **Step 5: Add `data-testid` + `data-active` to the settings button**

Still in `Sidebar.tsx`, find the bottom settings button (the one whose label is `t("nav.settings")` / navigates to the `"settings"` page). Add to that `<button>`:

```tsx
          data-testid="nav-settings"
          data-active={activePage === "settings"}
```

(Match the existing settings button's `onClick`/`aria-label`; only add the two attributes. If the settings click handler is `onClick={() => onPageChange("settings")}`, the `data-active` expression above is correct.)

- [ ] **Step 6: Run the smoke spec to verify it passes**

Run: `cd e2e && pnpm test tests/smoke.spec.ts`
Expected: PASS (2 tests).

- [ ] **Step 7: Verify frontend lint/tests unaffected**

Run: `cd frontend && pnpm lint && pnpm test`
Expected: lint clean, existing vitest suite still PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/App.tsx frontend/src/components/layout/Sidebar.tsx e2e/tests/smoke.spec.ts
git commit -m "✅ GUI e2e 冒烟:主布局与侧边栏导航"
```

---

## Task 4: Asset CRUD spec — create SSH asset → assert tree + DB row

**Files:**
- Create: `e2e/fixtures/db.ts`, `e2e/tests/asset-crud.spec.ts`
- Modify: `frontend/src/components/layout/AssetTree.tsx`, `frontend/src/components/asset/AssetForm.tsx`, `frontend/src/components/asset/SSHConfigSection.tsx`

- [ ] **Step 1: Write the DB oracle helper `e2e/fixtures/db.ts`**

```ts
import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";

export interface AssetRow {
  id: number;
  name: string;
  type: string;
  status: number;
}

// Opens the e2e temp opskat.db read-only and looks up an asset by name.
// Independent of the app's service layer — proves the row really hit disk.
// Uses Node's built-in node:sqlite (no native dependency).
export function findAssetByName(name: string): AssetRow | undefined {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (!dataDir) throw new Error("OPSKAT_DATA_DIR not set");
  const db = new DatabaseSync(join(dataDir, "opskat.db"), { readOnly: true });
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    const row = db
      .prepare("SELECT id, name, type, status FROM assets WHERE name = ?")
      .get(name);
    return row as AssetRow | undefined;
  } finally {
    db.close();
  }
}
```

> `node:sqlite` is stable on Node 26 and needs no flag. If `DatabaseSync`'s type isn't found by the installed `@types/node`, it's a type-only warning — Playwright runs the spec regardless. (Confirmed working: `node -e "require('node:sqlite')"` succeeds in this environment.)

- [ ] **Step 2: Write the failing CRUD spec `e2e/tests/asset-crud.spec.ts`**

```ts
import { test, expect } from "@playwright/test";
import { findAssetByName } from "../fixtures/db";

test("create SSH asset via UI persists to db and shows in tree", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-ssh-${Date.now()}`;

  await page.getByTestId("add-asset-button").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();

  // Default asset type is already "ssh"; fill the minimal required fields.
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("ssh-host-input").fill("example.com");
  await page.getByTestId("asset-form-submit").click();

  // Dialog closes and the new asset appears in the tree.
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(name)).toBeVisible();

  // Independent oracle: the row is actually persisted to opskat.db.
  await expect
    .poll(() => findAssetByName(name)?.status, { timeout: 10_000 })
    .toBe(1);
  const row = findAssetByName(name)!;
  expect(row.type).toBe("ssh");
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd e2e && pnpm test tests/asset-crud.spec.ts`
Expected: FAIL — `add-asset-button` / form test-ids / `asset-tree` not found.

- [ ] **Step 4: Add `data-testid` to the asset tree container + add button**

In `frontend/src/components/layout/AssetTree.tsx`:

Tree container — find the outermost tree `<div>`:
```tsx
    <div className="flex h-full w-full flex-col border-r border-panel-divider bg-sidebar">
```
Change to:
```tsx
    <div className="flex h-full w-full flex-col border-r border-panel-divider bg-sidebar" data-testid="asset-tree">
```

Add button — find the `<Button>` with `onClick={() => onAddAsset()}` and `aria-label={t("asset.addAsset")}`; add:
```tsx
              data-testid="add-asset-button"
```

- [ ] **Step 5: Add `data-testid` to the asset form dialog, name input, submit**

In `frontend/src/components/asset/AssetForm.tsx`:

Dialog content — find `<DialogContent className="sm:max-w-2xl ...">` and add `data-testid="asset-form-dialog"`.

Name input — find the `<Input>` bound to `value={name}` / `onChange={(e) => setName(e.target.value)}` and add `data-testid="asset-form-name-input"`.

Submit button — find `<Button onClick={handleSubmit} disabled={saveDisabled}>` and add `data-testid="asset-form-submit"`.

- [ ] **Step 6: Add `data-testid` to the SSH host input**

In `frontend/src/components/asset/SSHConfigSection.tsx`, find the host `<Input>` (bound to `value={state.host}` / `placeholder="example.com"`) and add:
```tsx
        data-testid="ssh-host-input"
```

- [ ] **Step 7: Run the CRUD spec to verify it passes**

Run: `cd e2e && pnpm test tests/asset-crud.spec.ts`
Expected: PASS.

- [ ] **Step 8: Verify frontend lint/tests unaffected**

Run: `cd frontend && pnpm lint && pnpm test`
Expected: lint clean, existing vitest suite still PASS.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/components/layout/AssetTree.tsx frontend/src/components/asset/AssetForm.tsx frontend/src/components/asset/SSHConfigSection.tsx e2e/fixtures/db.ts e2e/tests/asset-crud.spec.ts
git commit -m "✅ GUI e2e:经表单创建 SSH 资产并直查数据库校验落库"
```

---

## Task 5: `make test-e2e-gui` target + docs

**Files:**
- Modify: `Makefile`, `docs/testing-debugging-guide.md`

- [ ] **Step 1: Add the `test-e2e-gui` Make target**

In `Makefile`, near the existing `test-e2e` target, add:

```makefile
# GUI e2e（Playwright 驱动真实 wails dev，本地手动跑，不进 CI）
test-e2e-gui:
	cd e2e && pnpm install && pnpm exec playwright install chromium && pnpm test
```

- [ ] **Step 2: Verify the target runs the full suite**

Run: `make test-e2e-gui`
Expected: all three specs (`boot`, `smoke`, `asset-crud`) PASS; temp data dir is removed on teardown.

- [ ] **Step 3: Confirm hermeticity — real data dir untouched**

Run: `ls -la ~/Library/Application\ Support/opskat 2>/dev/null && echo "real dir still present/unchanged"`
Expected: the real data dir's mtime is not bumped by the test run (the suite used a `/tmp/opskat-e2e-*` dir). Confirm no `e2e-ssh-*` asset leaked into the real DB.

- [ ] **Step 4: Document the flow in `docs/testing-debugging-guide.md`**

Add a subsection under the automated-tests section:

````markdown
### GUI E2E (Playwright × Wails)

Drives the **real running app** through the Wails dev browser bridge. Local/manual only — not in CI.

**Prerequisites:** `wails` CLI on PATH, `pnpm`, Go toolchain, and a Chromium download (the make target runs `playwright install chromium`).

**Run:**

```bash
make test-e2e-gui
```

**What it does:** Playwright launches `make dev` with a temporary data dir + a fixed test master key + `OPSKAT_E2E=1` (disables the single-instance lock) + `OPSKAT_EXTENSIONS=0`. It opens `http://localhost:34115` (the Wails dev IPC bridge) in Chromium and runs:

- `boot.spec.ts` — app mounts via the bridge.
- `smoke.spec.ts` — main layout + sidebar navigation across all pages.
- `asset-crud.spec.ts` — create an SSH asset via the form, assert it shows in the asset tree, then verify persistence by a **direct read-only SQLite query** against the temp `opskat.db` (independent of the app's service layer).

**Isolation:** every run uses a fresh `/tmp/opskat-e2e-*` data dir, removed on teardown. The real `~/Library/Application Support/opskat` (or platform equivalent) is never touched, and the test master key is passed explicitly so the OS keychain is not read or written.

> A native window may open when `make dev` runs — that's expected; the test drives the `:34115` browser instance, not the native webview.
````

(Place it adjacent to the existing `make test-fixtures && make test-e2e` content; follow `docs/DOC-MAINTENANCE.md`.)

- [ ] **Step 5: Commit**

```bash
git add Makefile docs/testing-debugging-guide.md
git commit -m "📄 新增 make test-e2e-gui 目标与 GUI e2e 文档"
```

---

## Self-Review

**Spec coverage:**
- §2/§4.1 backend enabler (data dir / master key / single-instance bypass) → Task 1. ✔
- §4.2 frontend test-id seams → Tasks 3 & 4 (added incrementally where each spec needs them). ✔
- §4.3 e2e package layout (config, teardown, db helper, specs) → Tasks 2/3/4. ✔
- §4.4 persistence via direct SQLite query (`assets`, `type='ssh'`, `status=1`) → Task 4 db.ts + spec. ✔
- §4.5 make target + docs → Task 5. ✔
- §5 anti-flake (no sleeps; `expect`/`expect.poll`; extensions + single-instance off) → reflected in specs + config. ✔
- §6 verification (clean run, hermetic, real dir untouched) → Task 5 steps 2-3. ✔
- §1 non-goals (real SSH connect, CI, `audit_logs` hard-assert) → intentionally absent. ✔

**Placeholder scan:** No TBD/TODO; every code/edit step shows concrete content. ✔

**Type/name consistency:** test-id strings are identical across producer (frontend edits) and consumer (specs): `app-root`, `nav-<id>`/`nav-settings`, `asset-tree`, `add-asset-button`, `asset-form-dialog`, `asset-form-name-input`, `ssh-host-input`, `asset-form-submit`. `findAssetByName` signature matches its use. `resolveBootstrap` return tuple matches `main()` usage. ✔
