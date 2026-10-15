import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { CountTableRows, GetTableMetadata, OpenTable } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key}(${JSON.stringify(opts)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }) };
});

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value?: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

const header = (col: string) => document.querySelector(`[data-column-header-key="${col}"]`) as HTMLElement;

async function renderLoaded() {
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="orders" />);
  await waitFor(() => expect(header("name")).toBeTruthy());
}

describe("TableDataTab column metadata", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({
      tabs: [
        {
          id: "query-1",
          type: "query",
          label: "db",
          meta: { type: "query", assetId: 1, assetName: "db", assetIcon: "", assetType: "database", driver: "mysql" },
        },
      ],
      activeTabId: "query-1",
    });
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: ["orders"] },
          loadingTables: {},
          expandedDbs: ["appdb"],
          expandedSchemas: {},
          loadingDbs: false,
          innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "orders" }],
          activeInnerTabId: "table-1",
          error: null,
        },
      },
    });
    vi.mocked(OpenTable).mockReset();
    vi.mocked(CountTableRows).mockReset();
    vi.mocked(GetTableMetadata).mockReset();
    vi.mocked(CountTableRows).mockResolvedValue(1);
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: { id: "bigint", name: "varchar(64)" },
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 1,
        firstPage: [{ id: 1, name: "青木贸易" }],
        pageSize: 1000,
      })
    );
  });

  it("shows the real column comment, or a dash, once the metadata is loaded", async () => {
    vi.mocked(GetTableMetadata).mockResolvedValue({
      columns: [
        { name: "id", type: "bigint", nullable: false, primaryKey: true, comment: "订单编号" },
        { name: "name", type: "varchar(64)", nullable: true, primaryKey: false, comment: "" },
      ],
      indexes: [{ name: "idx_name", columns: ["name"], unique: false }],
      foreignKeys: [],
    } as never);
    await renderLoaded();
    await waitFor(() => expect(GetTableMetadata).toHaveBeenCalledWith(1, "appdb", "orders"));
    await waitFor(() => {
      fireEvent.contextMenu(header("id"), { clientX: 40, clientY: 20 });
      expect(screen.queryByText("query.showComment")).toBeTruthy();
    });
    fireEvent.click(screen.getByText("query.showComment"));
    expect(header("id")).toHaveTextContent("订单编号");
    expect(header("name")).toHaveTextContent("—");
  });

  it("omits the default row in the header card for a column the backend reports with no default (null)", async () => {
    const user = userEvent.setup();
    vi.mocked(GetTableMetadata).mockResolvedValue({
      columns: [
        { name: "id", type: "bigint", nullable: false, primaryKey: true, comment: "订单编号", default: null },
        { name: "name", type: "varchar(64)", nullable: true, primaryKey: false, comment: "", default: "" },
      ],
      indexes: [],
      foreignKeys: [],
    } as never);
    await renderLoaded();
    await waitFor(() => expect(GetTableMetadata).toHaveBeenCalled());
    const nameOf = (col: string) => header(col).querySelector("span.truncate") as HTMLElement;

    await waitFor(async () => {
      await user.hover(nameOf("id"));
      expect(await screen.findByRole("tooltip", {}, { timeout: 2000 })).toHaveTextContent("订单编号");
    });
    expect(screen.getByRole("tooltip")).not.toHaveTextContent("query.columnInfoDefault");
    fireEvent.pointerLeave(nameOf("id"));
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull());

    await user.hover(nameOf("name"));
    expect(await screen.findByRole("tooltip", {}, { timeout: 2000 })).toHaveTextContent("query.columnInfoDefault");
  });

  it("leaves the grid and the panel untouched when the metadata call fails", async () => {
    vi.mocked(GetTableMetadata).mockRejectedValue(new Error("permission denied"));
    await renderLoaded();
    await waitFor(() => expect(GetTableMetadata).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 50));

    fireEvent.contextMenu(header("id"), { clientX: 40, clientY: 20 });
    expect(screen.queryByText("query.showComment")).toBeNull();
    expect(header("id")).toHaveTextContent("id");
    expect(header("name")).toHaveTextContent("name");
    expect(useQueryStore.getState().dbStates["query-1"].error).toBeNull();
  });
});
