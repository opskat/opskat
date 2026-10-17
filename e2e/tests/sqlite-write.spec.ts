import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";
import { test, expect, type Locator, type Page } from "@playwright/test";

import { createSQLiteAssetViaUI } from "../fixtures/assets";

// SQLite 写路径:界面里的写操作经驱动生成语句、执行后,读回 SQLite 文件确认真实数据。

function seed(targetPath: string, statements: string[]): void {
  const target = new DatabaseSync(targetPath);
  for (const statement of statements) target.exec(statement);
  target.close();
}

function readBack<T>(targetPath: string, sql: string): T[] {
  const oracle = new DatabaseSync(targetPath, { readOnly: true });
  try {
    return oracle.prepare(sql).all() as T[];
  } finally {
    oracle.close();
  }
}

/** 建 SQLite 资产、打开它的查询面板并展开 main 库。 */
async function openPanel(page: Page, targetPath: string): Promise<Locator> {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
  const name = `e2e-sqlite-write-${Date.now()}`;
  await createSQLiteAssetViaUI(page, { name, path: targetPath });

  await page
    .getByTestId("asset-tree")
    .getByText(name, { exact: true })
    .dblclick();
  const panel = page.getByTestId("database-query-panel");
  await expect(panel).toBeVisible({ timeout: 30_000 });
  await panel.getByText("main", { exact: true }).click();
  return panel;
}

async function openTable(
  page: Page,
  targetPath: string,
  table: string,
): Promise<void> {
  const panel = await openPanel(page, targetPath);
  await panel.locator(`[data-table-node="main.${table}"]`).dblclick();
  await expect(page.locator('[data-cell-key="0:value"]')).toBeVisible({
    timeout: 30_000,
  });
}

const cell = (page: Page, key: string) =>
  page.locator(`[data-cell-key="${key}"]`);

async function typeIntoCell(
  page: Page,
  key: string,
  value: string,
): Promise<void> {
  await cell(page, key).dblclick();
  const input = cell(page, key).locator("input");
  await input.fill(value);
  await input.press("Enter");
}

/** 确认预览对话框里的语句并等它执行完、对话框关闭。 */
async function confirmPreview(page: Page): Promise<void> {
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toBeVisible();
  // 确认按钮的文案（query.confirmExecute）随界面语言。
  await dialog.getByRole("button", { name: /^(Execute|确认执行)$/ }).click();
  await expect(dialog).toBeHidden({ timeout: 30_000 });
}

test("edit a cell, add a row and delete a row in a SQLite table, then read the file back", async ({
  page,
}) => {
  const targetPath = join(
    process.env.OPSKAT_DATA_DIR!,
    `write-target-${Date.now()}.db`,
  );
  seed(targetPath, [
    "CREATE TABLE probes (id INTEGER PRIMARY KEY, value TEXT NOT NULL)",
    "INSERT INTO probes(value) VALUES ('alpha'), ('beta'), ('gamma')",
  ]);
  await openTable(page, targetPath, "probes");

  // 改单元格并保存。
  await typeIntoCell(page, "0:value", "alpha-edited");
  await page.getByTestId("table-apply-changes").click();
  await confirmPreview(page);
  await expect(page.getByTestId("table-apply-changes")).toBeDisabled();
  expect(
    readBack(targetPath, "SELECT id, value FROM probes ORDER BY id"),
  ).toEqual([
    { id: 1, value: "alpha-edited" },
    { id: 2, value: "beta" },
    { id: 3, value: "gamma" },
  ]);

  // 新增一行:自增主键不填,由数据库生成。
  await page.getByTestId("table-add-row").click();
  await cell(page, "3:id").locator("input").press("Escape");
  await typeIntoCell(page, "3:value", "delta");
  await page.getByTestId("table-apply-changes").click();
  await confirmPreview(page);
  await expect(page.getByTestId("table-apply-changes")).toBeDisabled();
  expect(
    readBack(targetPath, "SELECT id, value FROM probes WHERE value = 'delta'"),
  ).toEqual([{ id: 4, value: "delta" }]);

  // 删除一行。
  await page.locator('[data-row-header-key="1"]').click();
  await page.getByTestId("table-delete-rows").click();
  await confirmPreview(page);
  await expect(cell(page, "1:value")).toHaveText("gamma", { timeout: 30_000 });
  expect(
    readBack(targetPath, "SELECT id, value FROM probes ORDER BY id"),
  ).toEqual([
    { id: 1, value: "alpha-edited" },
    { id: 3, value: "gamma" },
    { id: 4, value: "delta" },
  ]);
});

test("create a table, then truncate and drop it, in a SQLite database, reading the schema back from the file", async ({
  page,
}) => {
  const targetPath = join(
    process.env.OPSKAT_DATA_DIR!,
    `schema-target-${Date.now()}.db`,
  );
  seed(targetPath, ["CREATE TABLE anchor (id INTEGER PRIMARY KEY)"]);
  const panel = await openPanel(page, targetPath);

  // 建表:语句由驱动生成,预览后执行。
  await panel.getByText("main", { exact: true }).click({ button: "right" });
  await page.getByTestId("database-add-table").click();
  await page.getByTestId("create-table-name").fill("made");
  await page.getByTestId("create-table-column-name").fill("id");
  await page
    .getByTestId("create-table-column-type")
    .fill("INTEGER PRIMARY KEY");
  await page.getByTestId("create-table-preview").click();
  await confirmPreview(page);
  await expect(panel.locator('[data-table-node="main.made"]')).toBeVisible({
    timeout: 30_000,
  });
  expect(
    readBack(
      targetPath,
      "SELECT name, type, pk FROM pragma_table_info('made')",
    ),
  ).toEqual([{ name: "id", type: "INTEGER", pk: 1 }]);

  // 清空:SQLite 没有 TRUNCATE,驱动发 DELETE FROM;表还在,行没了。
  seed(targetPath, ["INSERT INTO made(id) VALUES (1), (2)"]);
  await panel
    .locator('[data-table-node="main.made"]')
    .click({ button: "right" });
  await page.getByTestId("table-truncate").click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: /^(Truncate Table|清空表)$/ })
    .click();
  await expect(page.getByRole("alertdialog")).toBeHidden({ timeout: 30_000 });
  await expect
    .poll(() => readBack(targetPath, "SELECT COUNT(*) AS n FROM made"))
    .toEqual([{ n: 0 }]);

  // 删表。
  await panel
    .locator('[data-table-node="main.made"]')
    .click({ button: "right" });
  await page.getByTestId("table-drop").click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: /^(Drop Table|删除表)$/ })
    .click();
  await expect(panel.locator('[data-table-node="main.made"]')).toBeHidden({
    timeout: 30_000,
  });
  expect(
    readBack(
      targetPath,
      "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name",
    ),
  ).toEqual([{ name: "anchor" }]);
});
