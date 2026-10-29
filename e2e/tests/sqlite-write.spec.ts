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

/** 界面文案随语言:同时认英文与中文。 */
function text(en: string, zh: string): RegExp {
  const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`^(${escape(en)}|${escape(zh)})$`);
}

const FIELD_NAME = text("Name", "名称");

/** 字段表的数据行(表头没有名称输入框,不算在内),按表格次序。 */
const fieldRows = (page: Page) =>
  page
    .getByRole("row")
    .filter({ has: page.getByRole("textbox", { name: FIELD_NAME }) });

/** 「添加字段」:新行在最后,填上名称与类型。 */
async function addField(page: Page, name: string, type: string): Promise<void> {
  const before = await fieldRows(page).count();
  await page
    .getByRole("button", { name: text("Add column", "添加字段") })
    .click();
  await expect(fieldRows(page)).toHaveCount(before + 1);
  const row = fieldRows(page).nth(before);
  await row.getByRole("textbox", { name: FIELD_NAME }).fill(name);
  await row.getByRole("textbox", { name: text("Type", "类型") }).fill(type);
}

/** 切到索引子页、点「新建索引」,按次序加列;返回编辑区。 */
async function addIndex(page: Page, columns: string[]): Promise<Locator> {
  await page.getByRole("tab", { name: /^(Indexes|索引)/ }).click();
  await page
    .getByRole("button", { name: text("New index", "新建索引") })
    .click();
  const editor = page.getByTestId("designer-index-editor");
  await expect(editor).toBeVisible();
  for (const column of columns) {
    await editor
      .getByRole("button", { name: text("Add column", "添加列") })
      .click();
    await page
      .getByTestId("designer-index-column-list")
      .getByRole("option", { name: new RegExp(`^${column}\\b`) })
      .click();
  }
  await expect(editor.getByTestId("index-editor-column-name")).toHaveText(
    columns,
  );
  return editor;
}

/** 点设计器的保存(新建表是「创建」),在确认对话框里执行全部语句并等它关上。 */
async function saveDesign(
  page: Page,
  check?: (dialog: Locator) => Promise<void>,
): Promise<void> {
  const save = page.getByTestId("designer-save");
  // 语句由驱动按草稿生成,生成好之前保存不可用。
  await expect(save).toBeEnabled({ timeout: 30_000 });
  await save.click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog.getByTestId("save-statement").first()).toBeVisible();
  if (check) await check(dialog);
  await dialog
    .getByRole("button", { name: /^(Run statements \(\d+\)|执行 \d+ 条语句)$/ })
    .click();
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

test("design a SQLite table: rename it, rename and add columns, add an index, drop a column with its index, reading the schema back from the file", async ({
  page,
}) => {
  const targetPath = join(
    process.env.OPSKAT_DATA_DIR!,
    `alter-target-${Date.now()}.db`,
  );
  seed(targetPath, [
    "CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT)",
    "CREATE INDEX idx_people_email ON people(email)",
    "INSERT INTO people(name, email) VALUES ('alpha', 'a@example.com')",
  ]);
  const panel = await openPanel(page, targetPath);

  // 「设计表」打开设计器:列与索引由驱动读出。
  await panel
    .locator('[data-table-node="main.people"]')
    .click({ button: "right" });
  await page
    .getByRole("menuitem", { name: text("Design Table", "设计表") })
    .click();
  const tableName = page.getByTestId("designer-table-name-input");
  await expect(tableName).toHaveValue("people", { timeout: 30_000 });
  const nameOf = (i: number) =>
    fieldRows(page).nth(i).getByRole("textbox", { name: FIELD_NAME });
  await expect(nameOf(2)).toHaveValue("email");

  // 改表名、给列改名、加一列(默认值按「值」写)。
  await tableName.fill("persons");
  await nameOf(1).fill("full_name");
  await addField(page, "age", "INTEGER");
  const props = page.getByTestId("designer-column-props");
  await expect(props).toContainText("age");
  await props.getByRole("radio", { name: text("Value", "值") }).click();
  await props
    .getByRole("textbox", { name: text("Default value", "默认值内容") })
    .fill("18");

  // 删 email:它上面的索引随之删除。
  await fieldRows(page)
    .nth(2)
    .getByRole("button", { name: text("Delete column", "删除列") })
    .click();
  await expect(
    page.getByRole("row").filter({ hasText: "email" }),
  ).toHaveAttribute("data-status", "deleted");

  // 新索引:改名后的列加上新列(降序),名字按 idx_表_列 自动生成。
  const editor = await addIndex(page, ["full_name", "age"]);
  await editor
    .getByTestId("index-editor-column")
    .nth(1)
    .getByRole("button", { name: text("Descending", "降序") })
    .click();
  await expect(
    editor.getByRole("textbox", { name: text("Index name", "索引名") }),
  ).toHaveValue("idx_persons_full_name_age");
  await expect(
    page
      .getByTestId("designer-index-list")
      .getByRole("row")
      .filter({ hasText: "idx_people_email" }),
  ).toHaveAttribute("data-status", "deleted");

  // 确认对话框列出语句,并警告删除的列与随列删除的索引。
  await saveDesign(page, async (dialog) => {
    const warning = dialog.getByTestId("save-drop-warning");
    await expect(warning).toContainText("email");
    await expect(warning).toContainText("idx_people_email");
  });

  // 保存后重读:设计器跟随改名后的表,树上是新表名,没有剩余改动。
  await expect(panel.locator('[data-table-node="main.persons"]')).toBeVisible({
    timeout: 30_000,
  });
  await expect(tableName).toHaveValue("persons", { timeout: 30_000 });
  await expect(page.getByTestId("designer-changes")).toHaveText(
    text("No changes", "无改动"),
  );

  expect(
    readBack(
      targetPath,
      `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info('persons')`,
    ),
  ).toEqual([
    { name: "id", type: "INTEGER", notnull: 0, dflt_value: null, pk: 1 },
    { name: "full_name", type: "TEXT", notnull: 1, dflt_value: null, pk: 0 },
    { name: "age", type: "INTEGER", notnull: 0, dflt_value: "'18'", pk: 0 },
  ]);
  expect(readBack(targetPath, "SELECT full_name, age FROM persons")).toEqual([
    { full_name: "alpha", age: 18 },
  ]);
  // email 上的索引没了,只剩新索引;列按次序,age 降序。
  expect(
    readBack(
      targetPath,
      "SELECT name, tbl_name FROM sqlite_master WHERE type = 'index' ORDER BY name",
    ),
  ).toEqual([{ name: "idx_persons_full_name_age", tbl_name: "persons" }]);
  expect(
    readBack(
      targetPath,
      `SELECT name, "unique" FROM pragma_index_list('persons')`,
    ),
  ).toEqual([{ name: "idx_persons_full_name_age", unique: 0 }]);
  expect(
    readBack(
      targetPath,
      `SELECT name, "desc" FROM pragma_index_xinfo('idx_persons_full_name_age') WHERE key = 1 ORDER BY seqno`,
    ),
  ).toEqual([
    { name: "full_name", desc: 0 },
    { name: "age", desc: 1 },
  ]);
});

test("create a table with an index, then truncate and drop it, in a SQLite database, reading the schema back from the file", async ({
  page,
}) => {
  const targetPath = join(
    process.env.OPSKAT_DATA_DIR!,
    `schema-target-${Date.now()}.db`,
  );
  seed(targetPath, ["CREATE TABLE anchor (id INTEGER PRIMARY KEY)"]);
  const panel = await openPanel(page, targetPath);

  // 新建表:设计器从预置的自增主键 id 开始;加一列与其上的唯一索引,语句由驱动生成,确认后执行。
  await panel.getByText("main", { exact: true }).click({ button: "right" });
  await page.getByTestId("database-add-table").click();
  await expect(
    fieldRows(page).first().getByRole("textbox", { name: FIELD_NAME }),
  ).toHaveValue("id", { timeout: 30_000 });
  await page.getByTestId("designer-table-name-input").fill("made");
  await addField(page, "code", "TEXT");
  const editor = await addIndex(page, ["code"]);
  await editor.getByRole("radio", { name: text("Unique", "唯一") }).click();
  await expect(
    editor.getByRole("textbox", { name: text("Index name", "索引名") }),
  ).toHaveValue("idx_made_code");
  await saveDesign(page);

  // 建好后标签转为这张表的设计标签,读出刚建的索引。
  await expect(panel.locator('[data-table-node="main.made"]')).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByTestId("designer-table-name-input")).toHaveValue(
    "made",
    { timeout: 30_000 },
  );
  await page.getByRole("tab", { name: /^(Indexes|索引)/ }).click();
  await expect(page.getByTestId("designer-index-list")).toContainText(
    "idx_made_code",
  );
  expect(
    readBack(
      targetPath,
      "SELECT name, type, pk FROM pragma_table_info('made')",
    ),
  ).toEqual([
    { name: "id", type: "INTEGER", pk: 1 },
    { name: "code", type: "TEXT", pk: 0 },
  ]);
  expect(
    readBack(
      targetPath,
      `SELECT name, "unique" FROM pragma_index_list('made')`,
    ),
  ).toEqual([{ name: "idx_made_code", unique: 1 }]);
  expect(
    readBack(targetPath, "SELECT name FROM pragma_index_info('idx_made_code')"),
  ).toEqual([{ name: "code" }]);

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

test("import a CSV file into a SQLite table with append/update, then read the file back", async ({
  page,
}) => {
  const targetPath = join(
    process.env.OPSKAT_DATA_DIR!,
    `import-target-${Date.now()}.db`,
  );
  seed(targetPath, [
    "CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT, born DATE, avatar BLOB)",
    "INSERT INTO people(id, name) VALUES (1, 'alpha'), (2, 'beta')",
  ]);
  const panel = await openPanel(page, targetPath);
  await panel.locator('[data-table-node="main.people"]').dblclick();
  await expect(cell(page, "0:name")).toBeVisible({ timeout: 30_000 });

  // 导入向导:CSV,映射按列名自动对上,主键 id 作为匹配键,选「追加/更新」。
  await page.getByTitle(/^(Import Data|导入数据)$/).click();
  await page.getByTestId("import-type-csv").check();
  await page.getByTestId("import-wizard-next").click();
  await page.locator('input[type="file"]').setInputFiles({
    name: "people.csv",
    mimeType: "text/csv",
    buffer: Buffer.from(
      "id,name,born,avatar\n1,alpha-updated,24/08/2023,SGk=\n3,gamma,01/01/2024,NULL\n",
    ),
  });
  for (let step = 0; step < 4; step++) {
    await page.getByTestId("import-wizard-next").click();
  }
  await page.getByTestId("import-mode-append-update").check();
  await page.getByTestId("import-wizard-next").click();

  // 摘要里是驱动生成的语句:SQLite 的 upsert,日期已按源格式转换,二进制写成 X'…'。
  const statements = page.getByTestId("import-statement");
  await expect(statements).toHaveCount(1, { timeout: 30_000 });
  await expect(statements.first()).toHaveText(
    `INSERT INTO "main"."people" ("id", "name", "born", "avatar") VALUES ('1', 'alpha-updated', '2023-08-24', X'4869'), ('3', 'gamma', '2024-01-01', NULL) ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name", "born" = excluded."born", "avatar" = excluded."avatar";`,
  );
  await page.getByTestId("import-wizard-start").click();
  await expect(page.getByTestId("import-wizard-start")).toBeHidden({
    timeout: 30_000,
  });

  expect(
    readBack(
      targetPath,
      "SELECT id, name, born, hex(avatar) AS avatar FROM people ORDER BY id",
    ),
  ).toEqual([
    { id: 1, name: "alpha-updated", born: "2023-08-24", avatar: "4869" },
    { id: 2, name: "beta", born: null, avatar: "" },
    { id: 3, name: "gamma", born: "2024-01-01", avatar: "" },
  ]);
});
