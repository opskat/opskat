import { test, expect } from "@playwright/test";

test("app mounts via the wails dev bridge", async ({ page }) => {
  await page.goto("/");
  // Confirm we reached opskat (not another app on the port).
  await expect(page).toHaveTitle(/OpsKat/i);
  const root = page.locator("#root");
  await expect(root).toBeAttached();
  await expect(page.getByTestId("app-root")).toBeVisible();
});
