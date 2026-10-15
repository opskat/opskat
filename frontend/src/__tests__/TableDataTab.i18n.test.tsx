import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { OpenTable } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

function setupStores() {
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
          driver: "mysql",
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

describe("TableDataTab — new row placeholder", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    vi.mocked(OpenTable).mockReset();
    setupStores();
  });

  it("shows the default-value placeholder through i18n in a new row", async () => {
    const user = userEvent.setup();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: {},
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 1,
        firstPage: [{ id: 1, name: "ada" }],
        pageSize: 1000,
      })
    );
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
    await screen.findByText("ada");

    await user.click(screen.getByTitle("query.addRow"));

    expect(screen.getByText("query.defaultValuePlaceholder")).toBeInTheDocument();
  });
});
