import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { isMac } from "@/stores/shortcutStore";
import { OpenTable } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

function setupStores() {
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
}

describe("TableDataTab find in page", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    setupStores();
    vi.mocked(OpenTable).mockReset();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "customer"],
        columnTypes: {},
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 3,
        firstPage: [
          { id: 1, customer: "青木贸易" },
          { id: 2, customer: "北岭物流" },
          { id: 3, customer: "云栖科技" },
        ],
        pageSize: 1000,
      })
    );
  });

  it("opens from the grid, jumps to the match with the row detail following, and closes on JSON view", async () => {
    const user = userEvent.setup();
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="orders" />);
    await waitFor(() => expect(document.querySelector('[data-cell-key="1:customer"]')).toBeTruthy());
    await user.click(screen.getByTitle("query.rowDetail"));
    expect(screen.queryByText("query.rowDetailIndex")).toBeNull();

    const grid = document.querySelector(".query-table-scroll") as HTMLElement;
    fireEvent.keyDown(grid, { key: "f", code: "KeyF", metaKey: isMac, ctrlKey: !isMac });
    fireEvent.change(screen.getByPlaceholderText("query.find.placeholder"), { target: { value: "北岭" } });

    await waitFor(() => expect(screen.getByText("1 / 1")).toBeInTheDocument());
    expect(screen.getByText("query.find.scope")).toBeInTheDocument();
    expect(document.querySelector("mark[data-find-current]")?.closest("td")?.dataset.cellKey).toBe("1:customer");
    // the row detail panel follows the current match (it shows nothing until a row is current)
    expect(screen.getByText("query.rowDetailIndex")).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));
    expect(screen.queryByPlaceholderText("query.find.placeholder")).toBeNull();
  });
});
