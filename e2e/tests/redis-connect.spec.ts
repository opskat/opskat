import { test, expect } from "@playwright/test";
import { createRedisAssetViaUI } from "../fixtures/assets";

// Exercises a *second* asset type (Redis, beyond SSH) end-to-end AND the connect
// path: the app actually dials a live mock Redis (fixtures/redis-mock.mjs, started
// as a Playwright webServer) and the form's "Test Connection" (a single PING)
// succeeds. Covers the asset-type registration seam + a real, hermetic connection.
const MOCK_REDIS_PORT = process.env.MOCK_REDIS_PORT ?? "34217";

test("create a Redis asset, test-connect to the mock, and persist", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-redis-${Date.now()}`;

  await createRedisAssetViaUI(page, {
    name,
    host: "127.0.0.1",
    port: MOCK_REDIS_PORT,
    testConnection: true,
  });
});
