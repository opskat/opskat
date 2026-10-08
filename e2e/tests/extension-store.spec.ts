import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, type Page } from "@playwright/test";

// Settings → Extensions → Store, installing for real: the signed index and the OCI
// registry are the harness's extension-store mock (fixtures/ext-store-mock, plain
// http on the port block's +6), which the app reaches through the
// OPSKAT_E2E_EXT_INDEX_URL / _KEYS / _REGISTRY overrides (harness/env.js). The
// package is fixtures/store-ext, built and signed by the mock at startup.
//
// What the GUI must hold: the confirm comes first and is built from the index; a
// cancel downloads nothing; accepting pulls the package (progress), verifies it
// and installs it onto disk; a package whose sha256 differs from the index is
// refused, naming both digests, and nothing lands.

// The extension system starts on a background goroutine after boot (see
// extension-asset-type.spec.ts); the store installs through it.
const EXT_READY = 120_000;
test.describe.configure({ timeout: EXT_READY + 90_000 });

function extensionDir(name: string): string {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (!dataDir) throw new Error("OPSKAT_DATA_DIR not set");
  return join(dataDir, "extensions", name);
}

async function openStore(page: Page): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("language", "zh-CN"));
  // The installed list is read when the section mounts and shows the harness's
  // notebook only once the extension system is up, so reopen it until it does.
  await expect(async () => {
    await page.goto("/");
    await expect(page.getByTestId("app-root")).toBeVisible();
    await page.getByTestId("nav-settings").click();
    await page.getByRole("tab", { name: "扩展" }).click();
    await expect(page.getByText("笔记本", { exact: true })).toBeVisible({ timeout: 3_000 });
  }).toPass({ timeout: EXT_READY });
  await page.getByTestId("ext-view-store").click();
  await expect(page.getByTestId("ext-store-status")).toContainText("已验证签名");
}

test("a store install confirms from the index first, cancels without downloading, then downloads, verifies and installs", async ({
  page,
}) => {
  await openStore(page);
  const card = page.getByTestId("ext-store-card-store-demo");
  await expect(card.getByText("Store Demo")).toBeVisible();

  // Cancel: the confirm shows the index's description; declining lands nothing.
  await card.getByRole("button", { name: "安装" }).click();
  const dialog = page.getByTestId("ext-install-confirm-dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId("ext-install-version")).toHaveText("1.0.0");
  await expect(dialog).toContainText("官方商店，签名已验证");
  await expect(dialog.getByTestId("ext-install-cap-http")).toContainText("https://example.com/");
  await dialog.getByTestId("ext-install-cancel").click();
  await expect(dialog).toBeHidden();
  await expect(card.getByRole("button", { name: "安装" })).toBeEnabled();
  expect(existsSync(extensionDir("store-demo"))).toBe(false);

  // Accept: the download streams in slices, so its progress is on the card.
  await card.getByRole("button", { name: "安装" }).click();
  await dialog.getByTestId("ext-install-confirm").click();
  await expect(card.getByRole("progressbar")).toBeVisible();
  await expect(card.getByText(/下载中/)).toBeVisible();

  await expect(card.getByRole("button", { name: "已安装" })).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('[data-sonner-toast][data-type="success"]')).toContainText("扩展安装成功");
  // Disk oracle: the package landed as an installed extension.
  const manifest = JSON.parse(readFileSync(join(extensionDir("store-demo"), "manifest.json"), "utf8"));
  expect(manifest).toMatchObject({ name: "store-demo", version: "1.0.0" });
  expect(existsSync(join(extensionDir("store-demo"), "main.wasm"))).toBe(true);
});

test("a package whose sha256 differs from the signed index is refused with both digests and nothing lands", async ({
  page,
}) => {
  await openStore(page);
  const card = page.getByTestId("ext-store-card-store-tampered");
  await card.getByRole("button", { name: "安装" }).click();
  await page.getByTestId("ext-install-confirm").click();

  const alert = card.getByRole("alert");
  await expect(alert).toContainText("sha256 校验不符", { timeout: 60_000 });
  await expect(alert).toContainText("0".repeat(64));
  // The actual digest is the package the registry served: the same bytes
  // store-demo installed from.
  await expect(alert).toContainText(/[0-9a-f]{64}/);
  expect(existsSync(extensionDir("store-tampered"))).toBe(false);
  await expect(card.getByRole("button", { name: "安装" })).toBeEnabled();
  // Retry is offered; a mirror change is not — the registry answered, the bytes were wrong.
  await expect(alert.getByRole("button", { name: "重试" })).toBeVisible();
  await expect(alert.getByRole("button", { name: "更换镜像" })).toHaveCount(0);
});
