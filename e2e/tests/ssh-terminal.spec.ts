import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect } from "@playwright/test";

import { createSSHAssetViaUI } from "../fixtures/assets";

const SSH_MOCK_PORT = process.env.SSH_MOCK_PORT ?? "34218";

test("open an SSH terminal and observe a command executed by the remote shell", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-terminal-${Date.now()}`;
  const marker = `terminal-ran-${Date.now()}`;
  await createSSHAssetViaUI(page, { name, host: "127.0.0.1", port: SSH_MOCK_PORT });

  await page.getByTestId("asset-tree").getByText(name, { exact: true }).dblclick();
  const terminal = page.locator('[data-testid^="terminal-"]');
  const trust = page.getByTestId("ssh-host-key-trust");
  await expect.poll(async () => (await trust.isVisible()) || (await terminal.isVisible()), { timeout: 30_000 }).toBe(true);
  if (await trust.isVisible()) await trust.click();
  await expect(terminal).toBeVisible({ timeout: 30_000 });
  // xterm's helper textarea is intentionally visually hidden; clicking the
  // visible terminal surface lets xterm focus it through its own event path.
  await terminal.click();
  await page.keyboard.type(`echo ${marker}`);
  await page.keyboard.press("Enter");

  const commandLog = join(process.env.OPSKAT_DATA_DIR!, "ssh-mock.commands");
  await expect
    .poll(() => {
      try {
        return readFileSync(commandLog, "utf8");
      } catch {
        return "";
      }
    }, { timeout: 30_000 })
    .toContain(`echo ${marker}`);
});
