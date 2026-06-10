# e2e/scratch — throwaway functional-verification specs

Drop one-off `*.spec.ts` here to verify a feature you just finished, end-to-end, against the
**real running app**. Everything in this folder **except this README is gitignored** — these
scripts are not committed; write, run, observe, delete.

This is the GUI counterpart of "verify by observing": drive the real app, then read the
observable side-effects (UI assertions + the temp DB + logs). Full workflow, conventions, and
gotchas: **[docs/e2e-harness-guide.md](../../docs/e2e-harness-guide.md)** (§6).

## Run

```bash
make test-e2e-scratch        # runs every e2e/scratch/*.spec.ts via the live harness
# or a single file (still through the runner, so cleanup happens):
cd e2e && pnpm run test:scratch scratch/<file>.spec.ts
```

Reuses the same harness as the committed suite: launches `wails dev` on port 34216 with a
temp data dir + test master key + `OPSKAT_E2E=1` + `OPSKAT_EXTENSIONS=0`. A native OpsKat
window opens (expected). webServer output → `$TMPDIR/opskat-e2e-webserver.log`; the app's logs
+ `opskat.db` are under the temp data dir (`$OPSKAT_DATA_DIR`).

## Starter template

```ts
// e2e/scratch/verify-my-feature.spec.ts  (gitignored — delete when done)
import { test, expect } from "@playwright/test";
import { findAssetByName } from "../fixtures/db"; // read-only node:sqlite oracle; add helpers as needed

test("my feature works end-to-end", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  // 1. Drive the UI the way a user would (use data-testid locators + auto-wait, no sleeps).
  //    Add a data-testid in the component if you need a stable hook (additive only).

  // 2. Assert the UI reflects the change.

  // 3. Corroborate the side-effect independently — e.g. the row really hit the DB:
  // await expect.poll(() => findAssetByName("…")?.status).toBe(1);
});
```

If a flow proves to be **core and stable**, promote it: move the spec into `e2e/tests/`,
harden it, and commit (see the harness guide §5). Otherwise just delete it.
