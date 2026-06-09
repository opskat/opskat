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
  expect(findAssetByName(name)?.type).toBe("ssh");
});
