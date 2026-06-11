import { test, expect, type Page } from "@playwright/test";
import { findAssetByName } from "../fixtures/db";

// Completes the asset CRUD lifecycle the create-only `asset-crud` spec leaves
// open: edit (rename) and delete, each verified in the tree AND on disk via the
// independent DB oracle. Both drive the real right-click context menu.

async function createSshAsset(page: Page, name: string): Promise<void> {
  await page.getByTestId("add-asset-button").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("ssh-host-input").fill("example.com");
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(name, { exact: true })).toBeVisible();
  // Confirm it landed before we act on it, so a later failure can't be a create race.
  await expect.poll(() => findAssetByName(name)?.status, { timeout: 10_000 }).toBe(1);
}

test("edit renames an asset in the tree and on disk", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-edit-${Date.now()}`;
  const renamed = `${name}-renamed`;
  await createSshAsset(page, name);

  // Right-click the node → Edit → change the name → save (same form, pre-filled).
  await page.getByTestId("asset-tree").getByText(name, { exact: true }).click({ button: "right" });
  await page.getByTestId("asset-context-edit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();
  await page.getByTestId("asset-form-name-input").fill(renamed);
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();

  // UI shows the new name; DB oracle confirms the row was renamed (not duplicated).
  await expect(page.getByTestId("asset-tree").getByText(renamed, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(renamed)?.id, { timeout: 10_000 }).toBeTruthy();
  expect(findAssetByName(name)).toBeUndefined();
});

test("delete removes an asset from the tree and disk", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-del-${Date.now()}`;
  await createSshAsset(page, name);

  // Right-click the node → Delete → confirm in the shared ConfirmDialog.
  await page.getByTestId("asset-tree").getByText(name, { exact: true }).click({ button: "right" });
  await page.getByTestId("asset-context-delete").click();
  await page.getByTestId("confirm-delete-asset").click();

  // Gone from the tree; on disk it's a *soft* delete — the row stays but flips
  // StatusActive(1) → StatusDeleted(2), which the tree filters out. Assert that
  // real state change (the data-integrity contract), not row removal.
  await expect(page.getByTestId("asset-tree").getByText(name, { exact: true })).toHaveCount(0);
  await expect.poll(() => findAssetByName(name)?.status, { timeout: 10_000 }).toBe(2);
});
