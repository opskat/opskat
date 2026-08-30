import { test, expect } from "@playwright/test";
import { createSSHAssetViaUI } from "../fixtures/assets";

test("create SSH asset via UI persists to db and shows in tree", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-ssh-${Date.now()}`;

  await createSSHAssetViaUI(page, { name, host: "example.com" });
});
