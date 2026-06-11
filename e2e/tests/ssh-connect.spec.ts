import { test, expect } from "@playwright/test";
import { findAssetByName } from "../fixtures/db";

// Exercises the SSH connect path end-to-end: the app actually dials a live mock
// SSH server (fixtures/ssh-mock, NoClientAuth, started as a Playwright webServer)
// and the form's "Test Connection" completes a real SSH handshake. The SSH
// analog of redis-connect.spec.ts, and the GUI counterpart of the Go
// ssh_svc.TestConnection tests. SSH is the primary asset type, so its connect
// path — not just CRUD (asset-crud / asset-lifecycle) — is a core flow.
const SSH_MOCK_PORT = process.env.SSH_MOCK_PORT ?? "34218";

test("create an SSH asset, test-connect to the mock, and persist", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const name = `e2e-ssh-connect-${Date.now()}`;

  await page.getByTestId("add-asset-button").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();

  // Default type is ssh. Point host/port at the mock. canTest only needs host;
  // NoClientAuth means no credential is required for the handshake to succeed.
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("ssh-host-input").fill("127.0.0.1");
  await page.getByTestId("ssh-port-input").fill(String(SSH_MOCK_PORT));

  // "Test Connection" really dials the mock and completes the SSH handshake.
  // Success → a sonner toast; assert by data-type (locale-independent).
  await page.getByTestId("asset-test-connection").click();
  await expect(page.locator('[data-sonner-toast][data-type="success"]')).toBeVisible();

  // Persist and confirm it hit disk as an ssh-typed row.
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(name, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(name)?.type, { timeout: 10_000 }).toBe("ssh");
});
