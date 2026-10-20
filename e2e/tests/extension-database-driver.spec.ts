import { createServer, type Server, type Socket } from "node:net";
import { test, expect, type Page } from "@playwright/test";
import { openAssetForm } from "../fixtures/assets";
import { findAssetByName, findAssetPersistenceByName } from "../fixtures/db";

// A database driver provided by an extension is not a special species in the
// frontend: the driver dropdown, the asset form, the query panel and its
// "driver unavailable" state all read the backend's driver registry
// (Query.ListDatabaseDrivers), which the extension's load / disable / enable
// changes while the app keeps running. This spec drives that against the harness's
// fixture-dbdriver extension (pkg/extension/testdata/fixture-dbdriver, built and
// installed into the run's data dir by harness/env.js → installExtensions).
//
// The fixture's database is an in-memory catalog (database "app", tables "users" and
// "series"), but every connection logs in to the asset's host:port and announces
// each statement there first ("LOGIN …", "QUERY …", answered "OK"). The server below
// is that far end: what it receives is the observation that the statements really
// crossed the extension and the host's reach path, independent of what the GUI shows.

const EXT = "fixture-dbdriver";
const DRIVER = "fixturedb";

// The extension system compiles the guests' wasm on a background goroutine after
// startup, so the driver is registered a few seconds after the app answers (see
// extension-asset-type.spec.ts).
const EXT_READY = 120_000;
test.describe.configure({ timeout: EXT_READY + 120_000 });

let server: Server;
let port = 0;
const received: string[] = [];
const sockets = new Set<Socket>();

// A bare table reference (the default database): the table tab's own reads name
// "[app].[users]", so counting this text counts only the SQL editor's statements.
const USERS_SQL = "SELECT * FROM [users]";
const logins = () => received.filter((line) => line.startsWith("LOGIN")).length;
const usersQueries = () => received.filter((line) => line.startsWith("QUERY") && line.includes(USERS_SQL)).length;

test.beforeAll(async () => {
  server = createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    let buffered = "";
    socket.on("data", (chunk) => {
      buffered += chunk.toString("utf8");
      let newline: number;
      while ((newline = buffered.indexOf("\n")) >= 0) {
        received.push(buffered.slice(0, newline));
        buffered = buffered.slice(newline + 1);
        socket.write("OK\n");
      }
    });
    socket.on("error", () => sockets.delete(socket));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  port = (server.address() as { port: number }).port;
});

test.afterAll(async () => {
  // The app keeps its sessions open; close() alone would wait for them.
  for (const socket of sockets) socket.destroy();
  await new Promise<void>((resolve) => server.close(() => resolve()));
});

async function openApp(page: Page): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("language", "zh-CN"));
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
}

// The form may be opened before the extension finishes loading: the dropdown must
// pick the driver up when it registers, without reopening the form.
async function pickExtensionDriver(page: Page): Promise<void> {
  const select = page.getByTestId("database-driver-select");
  const option = page.getByTestId(`database-driver-option-${DRIVER}`);
  await expect(async () => {
    await select.click();
    try {
      await expect(option).toBeVisible({ timeout: 2_000 });
    } catch (e) {
      await page.keyboard.press("Escape");
      throw e;
    }
  }).toPass({ timeout: EXT_READY });
  await option.click();
}

async function setExtensionEnabled(page: Page, enabled: boolean): Promise<void> {
  await page.getByTestId("nav-settings").click();
  await page.getByRole("tab", { name: "扩展" }).click();
  const toggle = page.getByTestId(`extension-toggle-${EXT}`);
  await toggle.click();
  await expect(toggle).toHaveAttribute("data-state", enabled ? "checked" : "unchecked");
}

async function backToQueryTab(page: Page, name: string): Promise<void> {
  await page.locator(`[title="${name}"]`).first().click();
  await expect(page.getByTestId("database-query-panel")).toBeVisible();
}

async function runSql(page: Page, sql: string): Promise<void> {
  await page.getByTestId("database-new-sql-button").click();
  const editor = page.getByTestId("sql-editor").last();
  await editor.locator(".monaco-editor").click();
  await page.keyboard.insertText(sql);
  await page.getByTestId("sql-execute-button").last().click();
}

test("an extension's database driver: create an asset, browse and query, disable shows driver unavailable, enable recovers", async ({
  page,
}) => {
  await openApp(page);
  await openAssetForm(page);
  await page.getByTestId("asset-type-picker").click();
  await page.getByTestId("asset-type-option-database").click();
  await pickExtensionDriver(page);

  // The driver's descriptor shapes the form: its default port, and the built-in
  // fields it declares.
  const dialog = page.getByTestId("asset-form-dialog");
  await expect(dialog.getByTestId("database-port-input")).toHaveValue("7357");
  const name = `e2e-extdriver-${Date.now()}`;
  await page.getByTestId("asset-form-name-input").fill(name);
  await dialog.getByTestId("database-host-input").fill("127.0.0.1");
  await dialog.getByTestId("database-port-input").fill(String(port));
  await dialog.getByTestId("database-username-input").fill("e2e");
  await page.getByTestId("asset-form-submit").click();
  await expect(dialog).toBeHidden();

  await expect.poll(() => findAssetByName(name)?.type, { timeout: 10_000 }).toBe("database");
  expect(JSON.parse(findAssetPersistenceByName(name)!.config)).toMatchObject({
    driver: DRIVER,
    host: "127.0.0.1",
    port,
    username: "e2e",
  });

  // Browse: the tree lists the driver's database and tables, and a table opens with
  // its rows.
  await page.getByTestId("asset-tree").getByText(name, { exact: true }).dblclick();
  await expect(page.getByTestId("database-query-panel")).toBeVisible({ timeout: 30_000 });
  const usersNode = page.locator('[data-table-node="app.users"]');
  if (!(await usersNode.isVisible())) await page.getByText("app", { exact: true }).click();
  await usersNode.dblclick();
  await expect(page.getByText("carol", { exact: true })).toBeVisible({ timeout: 30_000 });
  expect(received).toContain("LOGIN e2e ");

  // Query: the statement crosses the extension to the asset's endpoint.
  const queriesBeforeSql = usersQueries();
  await runSql(page, USERS_SQL);
  await expect(page.getByText(/^3 行/).filter({ visible: true })).toBeVisible({ timeout: 30_000 });
  expect(usersQueries()).toBeGreaterThan(queriesBeforeSql);

  // Disable the extension: its driver leaves the registry, and the open query tab
  // says so instead of failing calls.
  await setExtensionEnabled(page, false);
  await backToQueryTab(page, name);
  const notices = page.getByTestId("database-query-panel").getByRole("alert");
  await expect(notices.first()).toContainText("驱动不可用");
  await expect(notices.first()).toContainText(DRIVER);

  // Enable it again: the same tab works again without restarting the app — the
  // tree is back, and a query runs through a new session of the re-registered
  // driver (disabling closed the old one).
  const loginsWhileDisabled = logins();
  await setExtensionEnabled(page, true);
  await backToQueryTab(page, name);
  await expect(notices).toHaveCount(0);
  await expect(page.locator('[data-table-node="app.users"]')).toBeVisible();
  const queriesBefore = usersQueries();
  await runSql(page, USERS_SQL);
  await expect(page.getByText(/^3 行/).filter({ visible: true })).toBeVisible({ timeout: 30_000 });
  expect(usersQueries()).toBeGreaterThan(queriesBefore);
  expect(logins()).toBeGreaterThan(loginsWhileDisabled);
});
