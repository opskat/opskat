# e2e/scratch — where a one-off verification keeps its evidence

One scenario, one directory `e2e/scratch/<scenario>/`, holding `report.md`, the `drive.log`
every `drive.mjs` call appends to, the screenshots `shot` writes there, and any oracle output
worth keeping. Everything in this folder **except this README is gitignored** — none of it is
committed.

**Most verifications author no script at all.** The sandbox holds the real app open and
you drive it command by command:

```bash
make dev-sandbox                                  # returns immediately; idempotent
node e2e/drive.mjs snapshot                       # look
node e2e/drive.mjs click add-asset-button         # act
node e2e/oracle.mjs audit --since=41              # observe
node e2e/drive.mjs shot result --scenario=my-fix  # record → e2e/scratch/my-fix/result.png
make dev-sandbox-down                             # when you're done
```

[docs/VERIFICATION.md](../../docs/VERIFICATION.md) owns that workflow and the report;
`drive.mjs --help` / `oracle.mjs --help` list the commands. Harness conventions and
gotchas: [docs/references/e2e-harness-guide.md](../../docs/references/e2e-harness-guide.md).

## When a spec is still the right answer

Write one only when the sequence must be **replayed** identically, or when **timing or
concurrency is the contract** — asserting *when* something happened is the thing you
cannot do by hand. Those run on the same hermetic harness as the committed suite:

```bash
cd e2e && pnpm run test:scratch scratch/<scenario>/verify.spec.ts   # only this scenario
make test-e2e-scratch                                              # every script left under scratch/
```

Always filter to your scenario — the bare target also runs whatever earlier rounds left behind.

That harness is a *different* run from the sandbox: it launches its own `wails dev` on its
own port with a temp data dir that **the runner deletes when Playwright exits**, so a spec
must capture its evidence during the run. The sandbox persists instead — that is why
interactive verification uses it. `node e2e/oracle.mjs where` prints the paths and ports
this checkout resolved to (they differ per worktree, so two can run at once).

```ts
// e2e/scratch/<scenario>/verify.spec.ts  (gitignored — delete when done)
import { test, expect } from "@playwright/test";
import { findAssetByName } from "../../fixtures/db"; // read-only node:sqlite oracle

test("my feature works end-to-end", async ({ page }) => {
  await page.goto("/");
  const name = `scratch-${Date.now()}`;
  await page.getByTestId("add-asset-button").click();
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("ssh-host-input").fill("example.com");
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-tree").getByText(name)).toBeVisible();  // UI
  await expect.poll(() => findAssetByName(name)?.status).toBe(1);              // DB oracle
});
```

If a flow proves to be **core and stable**, promote it: move the spec into `e2e/tests/`,
harden it, and commit (see the harness guide §5). Otherwise just delete the scenario directory.
