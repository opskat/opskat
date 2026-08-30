import { spawn } from "node:child_process";
import { test, expect } from "@playwright/test";

import { createRedisAssetViaUI } from "../fixtures/assets";
import { waitForAuditLogs } from "../fixtures/db";

const MOCK_REDIS_PORT = process.env.MOCK_REDIS_PORT ?? "34217";

function runOpsctl(args: string[]): Promise<{ code: number | null; stdout: string; stderr: string }> {
  const child = spawn("go", ["run", "./cmd/opsctl", ...args], {
    cwd: "..",
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => (stdout += chunk));
  child.stderr.setEncoding("utf8").on("data", (chunk) => (stderr += chunk));
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code) => resolve({ code, stdout, stderr }));
  });
}

test("a non-interactive opsctl command is approved by the desktop and audited", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const asset = `e2e-opsctl-${Date.now()}`;
  const key = `e2e:opsctl:${Date.now()}`;
  await createRedisAssetViaUI(page, { name: asset, host: "127.0.0.1", port: MOCK_REDIS_PORT });

  const resultPromise = runOpsctl([
    "--data-dir",
    process.env.OPSKAT_DATA_DIR!,
    "exec",
    asset,
    "--type",
    "redis",
    "--",
    "SET",
    key,
    "approved",
  ]);

  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("opsctl-approval-allow").click();

  const result = await resultPromise;
  expect(result, result.stderr).toMatchObject({ code: 0 });
  expect(JSON.parse(result.stdout)).toEqual({ type: "string", value: "OK" });

  const rows = await waitForAuditLogs({ assetName: asset, toolName: "exec" }, 1);
  expect(rows[0]).toMatchObject({
    source: "opsctl",
    command: `SET ${key} approved`,
    decision: "allow",
  });
});
