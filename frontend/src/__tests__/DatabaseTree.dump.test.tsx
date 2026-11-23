import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { DatabaseTree } from "../components/query/DatabaseTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { useAssetStore } from "../stores/assetStore";
import { ListDatabaseObjects, ListTables, SelectDumpFile, StartDumpSQLFile } from "../../wailsjs/go/query/Query";
import { dbdriver, query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

function openTab(driver: string, tables: Record<string, string[]> = { main: ["users", "orders"] }) {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "demo-db",
          assetIcon: "",
          assetType: "database",
          driver,
          defaultDatabase: "main",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: Object.keys(tables),
        tables,
        loadingTables: {},
        expandedDbs: Object.keys(tables),
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [],
        activeInnerTabId: null,
        error: null,
      },
    },
    redisStates: {},
    mongoStates: {},
  });
}

async function renderTree(driver: string, tables?: Record<string, string[]>) {
  openTab(driver, tables);
  render(<DatabaseTree tabId="query-1" />);
  await screen.findAllByText(/^query\.objTables/);
}

/** 表节点右键菜单里的「转储 SQL 文件」子菜单触发项;没有时为 null。 */
function dumpTrigger(table: string): HTMLElement | null {
  fireEvent.contextMenu(screen.getByText(table));
  return screen.queryByText("query.dumpSqlFile")?.closest<HTMLElement>("[role=menuitem]") ?? null;
}

/** 打开子菜单,返回它的菜单项文字。 */
function openSubmenu(trigger: HTMLElement): string[] {
  fireEvent.click(trigger);
  const submenu = trigger.parentElement!.querySelector<HTMLElement>("[role=menu]")!;
  return within(submenu)
    .getAllByRole("menuitem")
    .map((item) => item.textContent ?? "");
}

beforeEach(() => {
  useAssetStore.setState({ assets: [] });
  vi.mocked(ListDatabaseObjects).mockReset();
  vi.mocked(ListDatabaseObjects).mockResolvedValue(new query_svc.DatabaseObjects({}));
  vi.mocked(ListTables).mockReset();
  vi.mocked(ListTables).mockResolvedValue(["users", "orders"]);
  vi.mocked(SelectDumpFile).mockReset();
  vi.mocked(StartDumpSQLFile).mockReset();
});

describe("DatabaseTree 的「转储 SQL 文件」", () => {
  it("按驱动的声明出现:仅结构要能给出定义,结构和数据还要能生成行语句", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    await renderTree("sqlite");
    expect(openSubmenu(dumpTrigger("users")!)).toEqual([
      "query.dumpSqlFileStructureAndDataItem",
      "query.dumpSqlFileStructureOnlyItem",
    ]);

    cleanup();
    provideDrivers([fakeDriverWith(["view_ddl", "dump_sql"])]);
    await renderTree("fakedb");
    expect(openSubmenu(dumpTrigger("users")!)).toEqual(["query.dumpSqlFileStructureOnlyItem"]);

    cleanup();
    provideDrivers([fakeDriverWith(["view_ddl", "export_sql"])]);
    await renderTree("fakedb");
    expect(dumpTrigger("users")).toBeNull();

    cleanup();
    provideDrivers([fakeDriverWith(["dump_sql", "export_sql"])]);
    await renderTree("fakedb");
    expect(dumpTrigger("users")).toBeNull();
  });

  it("多选时转储全部选中的表,对话框的内容取自所选的子菜单项", async () => {
    provideDrivers([fakeDriverWith(["view_ddl", "export_sql", "dump_sql"])]);
    vi.mocked(SelectDumpFile).mockResolvedValue("/tmp/main.sql");
    vi.mocked(StartDumpSQLFile).mockResolvedValue("dump-1");
    await renderTree("fakedb");

    fireEvent.click(screen.getByText("users"));
    fireEvent.click(screen.getByText("orders"), { ctrlKey: true });
    const trigger = dumpTrigger("orders")!;
    openSubmenu(trigger);
    fireEvent.click(screen.getByText("query.dumpSqlFileStructureOnlyItem"));

    expect(await screen.findByText("query.dumpSqlFileTitle")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: /query\.dumpSqlFileOptionData\b/ })).not.toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: /query\.exportChooseFile/ }));
    await screen.findByText("/tmp/main.sql");
    fireEvent.click(screen.getByRole("button", { name: /query\.dumpSqlFileStart/ }));
    await waitFor(() =>
      expect(StartDumpSQLFile).toHaveBeenCalledWith(1, {
        database: "main",
        schema: "",
        tables: ["users", "orders"],
        whole: false,
        withData: false,
        options: {
          indexes: true,
          foreignKeys: true,
          views: false,
          routines: false,
          triggers: false,
          drops: false,
          disableForeignKeyChecks: false,
        },
        path: "/tmp/main.sql",
      })
    );
  });

  it("库节点:同一个子菜单转储整个库,下面是「运行 SQL 文件…」", async () => {
    provideDrivers([fakeDriverWith(["view_ddl", "export_sql", "dump_sql", "run_sql_file"])]);
    vi.mocked(SelectDumpFile).mockResolvedValue("/tmp/main.sql");
    vi.mocked(StartDumpSQLFile).mockResolvedValue("dump-1");
    await renderTree("fakedb");

    const trigger = dumpTrigger("main")!;
    const items = within(trigger.closest<HTMLElement>("[role=menu]")!)
      .getAllByRole("menuitem")
      .map((item) => item.getAttribute("data-testid"));
    expect(items.indexOf("database-dump-sql-file")).toBeLessThan(items.indexOf("database-run-sql-file"));
    expect(openSubmenu(trigger)).toEqual([
      "query.dumpSqlFileStructureAndDataItem",
      "query.dumpSqlFileStructureOnlyItem",
    ]);
    fireEvent.click(screen.getByText("query.dumpSqlFileStructureAndDataItem"));

    expect(await screen.findByText("query.dumpSqlFileTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /query\.exportChooseFile/ }));
    await screen.findByText("/tmp/main.sql");
    fireEvent.click(screen.getByRole("button", { name: /query\.dumpSqlFileStart/ }));
    await waitFor(() =>
      expect(StartDumpSQLFile).toHaveBeenCalledWith(1, {
        database: "main",
        schema: "",
        tables: [],
        whole: true,
        withData: true,
        options: {
          indexes: true,
          foreignKeys: true,
          views: true,
          routines: true,
          triggers: true,
          drops: false,
          disableForeignKeyChecks: false,
        },
        path: "/tmp/main.sql",
      })
    );
  });

  it("schema 节点(驱动按 schema 分组时):同一个子菜单转储整个 schema", async () => {
    provideDrivers([fakeDriverWith(["schemas", "view_ddl", "dump_sql"])]);
    vi.mocked(SelectDumpFile).mockResolvedValue("/tmp/sales.sql");
    vi.mocked(StartDumpSQLFile).mockResolvedValue("dump-1");
    await renderTree("fakedb", { main: ["public.users", "sales.orders"] });

    expect(openSubmenu(dumpTrigger("sales")!)).toEqual(["query.dumpSqlFileStructureOnlyItem"]);
    fireEvent.click(screen.getByText("query.dumpSqlFileStructureOnlyItem"));

    expect(await screen.findByText("query.dumpSqlFileTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /query\.exportChooseFile/ }));
    await screen.findByText("/tmp/sales.sql");
    fireEvent.click(screen.getByRole("button", { name: /query\.dumpSqlFileStart/ }));
    await waitFor(() =>
      expect(StartDumpSQLFile).toHaveBeenCalledWith(1, {
        database: "main",
        schema: "sales",
        tables: [],
        whole: true,
        withData: false,
        options: {
          indexes: true,
          foreignKeys: true,
          views: true,
          routines: true,
          triggers: true,
          drops: false,
          disableForeignKeyChecks: false,
        },
        path: "/tmp/sales.sql",
      })
    );
  });

  it("「关闭外键检查」按驱动描述的 dumpDisablesForeignKeyChecks 可选,不按驱动名判断", async () => {
    const checkbox = () => screen.getByRole("checkbox", { name: /query\.dumpSqlFileOptionForeignKeyChecks\b/ });
    provideDrivers([
      new dbdriver.Descriptor({
        ...fakeDriverWith(["view_ddl", "export_sql", "dump_sql"]),
        dumpDisablesForeignKeyChecks: true,
      }),
    ]);
    await renderTree("fakedb");
    openSubmenu(dumpTrigger("users")!);
    fireEvent.click(screen.getByText("query.dumpSqlFileStructureOnlyItem"));
    expect(await screen.findByText("query.dumpSqlFileTitle")).toBeInTheDocument();
    expect(checkbox()).toBeEnabled();
    expect(checkbox()).toBeChecked();

    cleanup();
    provideDrivers([fakeDriverWith(["view_ddl", "export_sql", "dump_sql"])]);
    await renderTree("fakedb");
    openSubmenu(dumpTrigger("users")!);
    fireEvent.click(screen.getByText("query.dumpSqlFileStructureOnlyItem"));
    expect(await screen.findByText("query.dumpSqlFileTitle")).toBeInTheDocument();
    expect(checkbox()).toBeDisabled();
    expect(checkbox()).not.toBeChecked();
  });

  it("库节点与 schema 节点在驱动不能转储时没有这一项", async () => {
    provideDrivers([fakeDriverWith(["schemas", "view_ddl", "export_sql"])]);
    await renderTree("fakedb", { main: ["public.users"] });
    expect(dumpTrigger("main")).toBeNull();
    cleanup();
    provideDrivers([fakeDriverWith(["schemas", "view_ddl", "export_sql"])]);
    await renderTree("fakedb", { main: ["public.users"] });
    expect(dumpTrigger("public")).toBeNull();
  });

  it("选中的表跨了库时不能转储,悬停说明原因", async () => {
    provideDrivers([fakeDriverWith(["view_ddl", "export_sql", "dump_sql"])]);
    await renderTree("fakedb", { main: ["users"], reporting: ["events"] });

    fireEvent.click(screen.getByText("users"));
    fireEvent.click(screen.getByText("events"), { ctrlKey: true });
    const trigger = dumpTrigger("events")!;
    expect(trigger).toHaveAttribute("data-disabled");
    expect(trigger.closest("[title]")).toHaveAttribute("title", "query.dumpSqlFileOneDatabaseHint");
  });

  it("子菜单可以用键盘打开与选择", async () => {
    provideDrivers([fakeDriverWith(["view_ddl", "export_sql", "dump_sql"])]);
    await renderTree("fakedb");

    const trigger = dumpTrigger("users")!;
    const menu = trigger.closest<HTMLElement>("[role=menu]")!;
    // 方向键进入菜单,逐项移到子菜单的触发项。
    let guard = 0;
    while (document.activeElement !== trigger && guard++ < 20) fireEvent.keyDown(document, { key: "ArrowDown" });
    expect(document.activeElement).toBe(trigger);
    expect(menu).toContainElement(trigger);

    fireEvent.keyDown(trigger, { key: "ArrowRight" });
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    await waitFor(() => expect(document.activeElement).toHaveTextContent("query.dumpSqlFileStructureAndDataItem"));
    fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
    expect(document.activeElement).toHaveTextContent("query.dumpSqlFileStructureOnlyItem");
    fireEvent.keyDown(document.activeElement!, { key: "ArrowLeft" });
    expect(document.activeElement).toBe(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "false");

    fireEvent.keyDown(trigger, { key: "Enter" });
    await waitFor(() => expect(document.activeElement).toHaveTextContent("query.dumpSqlFileStructureAndDataItem"));
    fireEvent.keyDown(document.activeElement!, { key: "Enter" });
    expect(await screen.findByText("query.dumpSqlFileTitle")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: /query\.dumpSqlFileOptionData\b/ })).toBeChecked();
  });
});
