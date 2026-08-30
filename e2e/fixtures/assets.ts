import { expect, type Page } from "@playwright/test";

import { findAssetByName } from "./db";

export async function openAssetForm(page: Page): Promise<void> {
  await page.getByTestId("add-asset-button").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();
}

export async function createSSHAssetViaUI(
  page: Page,
  options: { name: string; host: string; port?: string; testConnection?: boolean }
): Promise<void> {
  await openAssetForm(page);
  await page.getByTestId("asset-form-name-input").fill(options.name);
  await page.getByTestId("ssh-host-input").fill(options.host);
  if (options.port) await page.getByTestId("ssh-port-input").fill(options.port);
  if (options.testConnection) {
    await page.getByTestId("asset-test-connection").click();
    await expect(page.locator('[data-sonner-toast][data-type="success"]')).toBeVisible();
  }
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(options.name, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(options.name)?.type, { timeout: 10_000 }).toBe("ssh");
}

export async function createSQLiteAssetViaUI(
  page: Page,
  options: { name: string; path: string }
): Promise<void> {
  await openAssetForm(page);
  await page.getByTestId("asset-type-picker").click();
  await page.getByTestId("asset-type-option-database").click();
  await page.getByTestId("asset-form-name-input").fill(options.name);
  await page.getByTestId("database-driver-select").click();
  await page.getByTestId("database-driver-option-sqlite").click();
  await page.getByTestId("database-sqlite-path-input").fill(options.path);
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(options.name, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(options.name)?.type, { timeout: 10_000 }).toBe("database");
}

export async function createRedisAssetViaUI(
  page: Page,
  options: { name: string; host: string; port: string; testConnection?: boolean }
): Promise<void> {
  await openAssetForm(page);
  await page.getByTestId("asset-type-picker").click();
  await page.getByTestId("asset-type-option-redis").click();
  await page.getByTestId("asset-form-name-input").fill(options.name);
  await page.getByTestId("redis-host-input").fill(options.host);
  await page.getByTestId("redis-port-input").fill(options.port);
  if (options.testConnection) {
    await page.getByTestId("asset-test-connection").click();
    await expect(page.locator('[data-sonner-toast][data-type="success"]')).toBeVisible();
  }
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(options.name, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(options.name)?.type, { timeout: 10_000 }).toBe("redis");
}
