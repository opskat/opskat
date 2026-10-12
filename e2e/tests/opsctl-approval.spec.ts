import { test, expect } from "@playwright/test";

import { createRedisAssetViaUI } from "../fixtures/assets";
import { waitForAuditLogs } from "../fixtures/db";
import { runOpsctl } from "../fixtures/opsctl";

const MOCK_REDIS_PORT = process.env.MOCK_REDIS_PORT ?? "34217";

test("a non-interactive opsctl command is approved by the desktop and audited", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const asset = `e2e-opsctl-${Date.now()}`;
  const key = `e2e:opsctl:${Date.now()}`;
  await createRedisAssetViaUI(page, { name: asset, host: "127.0.0.1", port: MOCK_REDIS_PORT });

  const resultPromise = runOpsctl([
    "--data-dir",
    process.env.OPSKAT_DATA_DIR!,
    "exec",
    asset,
    "--type",
    "redis",
    "--",
    "SET",
    key,
    "approved",
  ]);

  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("opsctl-approval-allow").click();

  const result = await resultPromise;
  expect(result, result.stderr).toMatchObject({ code: 0 });
  expect(JSON.parse(result.stdout)).toEqual({ type: "string", value: "OK" });

  const rows = await waitForAuditLogs({ assetName: asset, toolName: "exec" }, 1);
  expect(rows[0]).toMatchObject({
    source: "opsctl",
    command: `SET ${key} approved`,
    decision: "allow",
  });
});

// A scripted `opsctl a && opsctl b` raises the second approval a moment after the first
// is answered — while the first dialog is still animating out. The dialog has to come
// back clickable: if the backdrop is re-mounted above the still-mounted panel, the
// buttons are there but no mouse can reach them, and the CLI hangs until it times out.
test("an approval arriving while the previous dialog is closing can still be clicked", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  // Sweep the whole exit animation: which moment is fragile depends on how the panel's
  // and the backdrop's exit timings relate, and that is exactly what must not matter.
  for (const delay of [40, 90, 130, 160, 175, 190, 205, 240]) {
    // Raise one approval; once it is up, answer it and raise the next `delay` ms later.
    // The gap is timed inside the page, where the animation runs.
    await page.evaluate(
      ([ms, tag]) => {
        const raise = (id: string) =>
          (
            window as unknown as { runtime: { EventsEmit: (name: string, data: unknown) => void } }
          ).runtime.EventsEmit("opsctl:approval", {
            confirm_id: id,
            kind: "once",
            type: "exec",
            asset_id: 1,
            asset_name: "e2e",
            command: `echo ${id}`,
            session_id: "e2e",
          });
        const deny = () => document.querySelector<HTMLElement>('[data-testid="opsctl-approval-deny"]');
        raise(`first-${tag}`);
        const waitForFirst = setInterval(() => {
          const button = deny();
          if (!button) return;
          clearInterval(waitForFirst);
          // Let the opening animation finish before answering.
          setTimeout(() => {
            button.click();
            setTimeout(() => raise(`second-${tag}`), ms);
          }, 400);
        }, 20);
      },
      [delay, String(delay)] as const
    );

    await expect(page.getByTestId("approval-command-text")).toContainText(`second-${delay}`);
    // A real pointer click: Playwright refuses it when another element would receive it.
    await page.getByTestId("opsctl-approval-deny").click({ timeout: 3_000 });
    await expect(page.getByTestId("opsctl-approval-dialog")).toBeHidden();
  }
});
