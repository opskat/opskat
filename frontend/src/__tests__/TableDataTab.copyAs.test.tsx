import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { GetRowStatements, OpenTable } from "../../wailsjs/go/query/Query";
import { fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

const writeText = vi.fn();

async function renderTable() {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 7, assetName: "db", assetIcon: "", assetType: "database", driver: "fakedb" },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["public.a.b"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "public.a.b" }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="public.a.b" />);
  await waitFor(() => expect(screen.getByText("alice")).toBeInTheDocument());
}

function copyAs(cellText: string, item: "copyAsInsert" | "copyAsUpdate") {
  fireEvent.contextMenu(screen.getByText(cellText));
  fireEvent.click(screen.getByText(`query.${item}`));
}

// 复制为 INSERT / UPDATE 的语句由驱动生成:界面把行交给 GetRowStatements(表名原样,不拼限定形式),
// 写进剪贴板的正是它返回的语句。
describe("TableDataTab 复制为 SQL", () => {
  beforeEach(() => {
    provideDrivers([fakeDriverWith(["copy_sql"])]);
    writeText.mockReset().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.mocked(GetRowStatements).mockReset();
    vi.mocked(OpenTable).mockReset();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: { id: "int", name: "text" },
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 2,
        firstPage: [
          { id: 1, name: "alice" },
          { id: 2, name: "中文" },
        ],
        pageSize: 1000,
      })
    );
  });

  it("复制为 INSERT:剪贴板里是驱动返回的语句", async () => {
    vi.mocked(GetRowStatements).mockResolvedValue(["DRIVER INSERT ONE;"]);
    await renderTable();

    copyAs("alice", "copyAsInsert");

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("DRIVER INSERT ONE;"));
    expect(GetRowStatements).toHaveBeenCalledWith(7, "appdb", "public.a.b", {
      kind: "insert",
      columns: ["id", "name"],
      rows: [{ id: 1, name: "alice" }],
      primaryKeys: ["id"],
    });
  });

  it("复制为 UPDATE:按主键定位,剪贴板里是驱动返回的语句", async () => {
    vi.mocked(GetRowStatements).mockResolvedValue(["DRIVER UPDATE;"]);
    await renderTable();

    copyAs("中文", "copyAsUpdate");

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("DRIVER UPDATE;"));
    expect(GetRowStatements).toHaveBeenCalledWith(7, "appdb", "public.a.b", {
      kind: "update",
      columns: ["id", "name"],
      rows: [{ id: 2, name: "中文" }],
      primaryKeys: ["id"],
    });
  });

  it("驱动生成失败:报错,不写剪贴板", async () => {
    const toastError = vi.spyOn(toast, "error");
    vi.mocked(GetRowStatements).mockRejectedValue(new Error("query columns: no such table"));
    await renderTable();

    copyAs("alice", "copyAsInsert");

    await waitFor(() => expect(toastError).toHaveBeenCalledWith(expect.stringContaining("no such table")));
    expect(writeText).not.toHaveBeenCalled();
    toastError.mockRestore();
  });
});
