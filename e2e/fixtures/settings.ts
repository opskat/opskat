import { expect, type Page } from "@playwright/test";

/**
 * Opens the settings page on one section of its left navigation. `section` is the
 * nav item's id (`settings-nav-<id>` in SettingsPage.tsx: "extensions",
 * "custom-types", …), so the helper works whatever language the spec runs in.
 */
export async function openSettingsSection(page: Page, section: string): Promise<void> {
  await page.getByTestId("nav-settings").click();
  const item = page.getByTestId(`settings-nav-${section}`);
  await item.click();
  await expect(item).toHaveAttribute("aria-current", "page");
}
