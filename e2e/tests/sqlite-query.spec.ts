import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";
import { test, expect } from "@playwright/test";

import { createSQLiteAssetViaUI } from "../fixtures/assets";

test("run a SQLite query through the database workspace and render the persisted row", async ({ page }) => {
  const targetPath = join(process.env.OPSKAT_DATA_DIR!, `query-target-${Date.now()}.db`);
  const target = new DatabaseSync(targetPath);
  target.exec("CREATE TABLE probes (id INTEGER PRIMARY KEY, value TEXT NOT NULL)");
  target.prepare("INSERT INTO probes(value) VALUES (?)").run("sqlite-e2e-value");
  target.close();

  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
  const name = `e2e-sqlite-${Date.now()}`;
  await createSQLiteAssetViaUI(page, { name, path: targetPath });

  await page.getByTestId("asset-tree").getByText(name, { exact: true }).dblclick();
  await expect(page.getByTestId("database-query-panel")).toBeVisible({ timeout: 30_000 });
  await page.getByTestId("database-new-sql-button").click();
  const editor = page.getByTestId("sql-editor");
  await editor.locator(".monaco-editor").click();
  await page.keyboard.insertText("SELECT id, value FROM probes");
  await page.getByTestId("sql-execute-button").click();

  await expect(page.getByText("sqlite-e2e-value", { exact: true })).toBeVisible({ timeout: 30_000 });

  const oracle = new DatabaseSync(targetPath, { readOnly: true });
  expect(oracle.prepare("SELECT value FROM probes WHERE id = 1").get()).toEqual({ value: "sqlite-e2e-value" });
  oracle.close();
});
