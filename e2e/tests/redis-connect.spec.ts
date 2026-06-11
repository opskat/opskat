import { test, expect } from "@playwright/test";
import { findAssetByName } from "../fixtures/db";

// Exercises a *second* asset type (Redis, beyond SSH) end-to-end AND the connect
// path: the app actually dials a live mock Redis (fixtures/redis-mock.mjs, started
// as a Playwright webServer) and the form's "Test Connection" (a single PING)
// succeeds. Covers the asset-type registration seam + a real, hermetic connection.
const MOCK_REDIS_PORT = process.env.MOCK_REDIS_PORT ?? "34217";

test("create a Redis asset, test-connect to the mock, and persist", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-redis-${Date.now()}`;

  await page.getByTestId("add-asset-button").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();

  // Switch the type from the default ssh → redis (drives the AssetTypePicker).
  await page.getByTestId("asset-type-picker").click();
  await page.getByTestId("asset-type-option-redis").click();

  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("redis-host-input").fill("127.0.0.1");
  await page.getByTestId("redis-port-input").fill(String(MOCK_REDIS_PORT));

  // "Test Connection" really dials the mock (PING → +PONG). On success the app
  // shows a sonner toast; assert by data-type (locale-independent) rather than text.
  await page.getByTestId("asset-test-connection").click();
  await expect(page.locator('[data-sonner-toast][data-type="success"]')).toBeVisible();

  // Persist and confirm it hit disk as a redis-typed row.
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(name, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(name)?.type, { timeout: 10_000 }).toBe("redis");
});
