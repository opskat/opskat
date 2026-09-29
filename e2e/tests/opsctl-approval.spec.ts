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

test("a second approval arriving right after the first is answered stays clickable", async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const asset = `e2e-opsctl-b2b-${Date.now()}`;
  await createRedisAssetViaUI(page, { name: asset, host: "127.0.0.1", port: MOCK_REDIS_PORT });

  const run = (key: string) =>
    runOpsctl(["--data-dir", process.env.OPSKAT_DATA_DIR!, "exec", asset, "--type", "redis", "--", "SET", key, "v"]);

  const first = run(`e2e:b2b:1:${Date.now()}`);
  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("opsctl-approval-allow").click();
  // 不等第一个弹窗退场完毕就启动第二条：队列清空后紧接着又来一条审批。
  const second = run(`e2e:b2b:2:${Date.now()}`);

  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
  // 不用 force：被盖在上方的遮罩拦截时，点击会超时失败。
  await page.getByTestId("opsctl-approval-allow").click({ timeout: 10_000 });

  for (const pending of [first, second]) {
    const result = await pending;
    expect(result, result.stderr).toMatchObject({ code: 0 });
  }
});
