import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { OpenTable } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

function setupStores(driver: string) {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "db",
          assetIcon: "",
          assetType: "database",
          driver,
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["users"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "users" }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
}

async function renderOpened(driver: string) {
  setupStores(driver);
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
  await waitFor(() => expect(screen.getByText("alice")).toBeInTheDocument());
}

const present = (title: string) => screen.queryByTitle(title) !== null;

function gridEditEntries() {
  return {
    submit: present("query.submitEdits"),
    discard: present("query.discardEdits"),
    preview: present("query.previewSql"),
    addRow: present("query.addRow"),
    deleteRecord: present("query.deleteRecord"),
    apply: present("query.applyChanges"),
    discardChanges: present("query.discardChanges"),
  };
}

async function sqlExportOptionShown(): Promise<boolean> {
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox"));
  const csv = await screen.findAllByText("CSV");
  expect(csv.length).toBeGreaterThan(0);
  return screen.queryAllByText("SQL").length > 0;
}

function copyAsShown(): boolean {
  fireEvent.contextMenu(screen.getByText("alice"));
  return screen.queryByText("query.copyAs") !== null;
}

describe("TableDataTab 的功能入口", () => {
  beforeEach(() => {
    vi.mocked(OpenTable).mockReset();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: { id: "int", name: "text" },
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 1,
        firstPage: [{ id: 1, name: "alice" }],
        pageSize: 1000,
      })
    );
  });

  for (const id of ["mysql", "postgresql", "mssql", "sqlite"]) {
    it(`内置驱动 ${id}:查看建表语句、表格编辑、导入、导出为 SQL、复制为 SQL 都在`, async () => {
      provideDrivers(BUILTIN_DRIVERS);
      await renderOpened(id);

      expect(screen.queryByText("query.viewDDL")).toBeInTheDocument();
      expect(gridEditEntries()).toEqual({
        submit: true,
        discard: true,
        preview: true,
        addRow: true,
        deleteRecord: true,
        apply: true,
        discardChanges: true,
      });
      expect(present("query.importData")).toBe(true);
      expect(await sqlExportOptionShown()).toBe(true);
      expect(copyAsShown()).toBe(true);
    });
  }

  it("驱动什么都不声明:这些入口都不出现,只剩读路径上的功能", async () => {
    provideDrivers([fakeDriverWith([])]);
    await renderOpened("fakedb");

    expect(screen.queryByText("query.viewDDL")).not.toBeInTheDocument();
    expect(Object.values(gridEditEntries()).some(Boolean)).toBe(false);
    expect(present("query.importData")).toBe(false);
    expect(present("query.exportData")).toBe(true);
    expect(await sqlExportOptionShown()).toBe(false);
    expect(copyAsShown()).toBe(false);
    // 翻页、刷新仍然可用。
    expect(present("query.nextPage")).toBe(true);
  });

  it("view_ddl 只管「查看建表语句」", async () => {
    provideDrivers([fakeDriverWith(["view_ddl"])]);
    await renderOpened("fakedb");

    expect(screen.queryByText("query.viewDDL")).toBeInTheDocument();
    expect(Object.values(gridEditEntries()).some(Boolean)).toBe(false);
    expect(present("query.importData")).toBe(false);
  });

  it("grid_edit 只管表格编辑", async () => {
    provideDrivers([fakeDriverWith(["grid_edit"])]);
    await renderOpened("fakedb");

    expect(Object.values(gridEditEntries()).every(Boolean)).toBe(true);
    expect(screen.queryByText("query.viewDDL")).not.toBeInTheDocument();
    expect(present("query.importData")).toBe(false);
    expect(await sqlExportOptionShown()).toBe(false);
  });

  it("import 只管导入", async () => {
    provideDrivers([fakeDriverWith(["import"])]);
    await renderOpened("fakedb");

    expect(present("query.importData")).toBe(true);
    expect(Object.values(gridEditEntries()).some(Boolean)).toBe(false);
  });

  it("export_sql 只管导出格式里的 SQL", async () => {
    provideDrivers([fakeDriverWith(["export_sql"])]);
    await renderOpened("fakedb");

    expect(await sqlExportOptionShown()).toBe(true);
    expect(present("query.importData")).toBe(false);
  });

  it("copy_sql 只管「复制为」", async () => {
    provideDrivers([fakeDriverWith(["copy_sql"])]);
    await renderOpened("fakedb");

    expect(copyAsShown()).toBe(true);
    expect(await sqlExportOptionShown()).toBe(false);
  });
});
