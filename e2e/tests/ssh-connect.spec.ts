import { test, expect } from "@playwright/test";
import { createSSHAssetViaUI } from "../fixtures/assets";

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

  await createSSHAssetViaUI(page, {
    name,
    host: "127.0.0.1",
    port: SSH_MOCK_PORT,
    testConnection: true,
  });
});
