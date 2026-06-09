import { test, expect } from "@playwright/test";

const NAV_IDS = ["home", "forward", "sshkeys", "snippets", "audit", "settings"] as const;

test("main layout renders", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
});

test("sidebar navigates across all pages", async ({ page }) => {
  await page.goto("/");
  for (const id of NAV_IDS) {
    const btn = page.getByTestId(`nav-${id}`);
    await btn.click();
    await expect(btn).toHaveAttribute("data-active", "true");
    // App survives navigation to every page.
    await expect(page.getByTestId("app-root")).toBeVisible();
  }
});
