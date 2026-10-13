import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQL, ExecuteSQLPaged, GetTableDDL, OpenTable } from "../../wailsjs/go/query/Query";
import { FAKE_DRIVER } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

// 「查看建表语句」发给后端的是库名与表名,建表语句由资产的驱动生成。这里用一个前端一无所知的
// 驱动名:没有任何一步该、也没法为它拼 SQL。

function setup(table: string) {
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
          driver: FAKE_DRIVER.id,
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: [table] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
}

async function openDDL(table: string) {
  const user = userEvent.setup();
  setup(table);
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table={table} />);
  await user.click(await screen.findByText("query.viewDDL"));
}

describe("TableDataTab view DDL", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQLPaged).mockReset();
    vi.mocked(GetTableDDL).mockReset();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id"],
        columnTypes: {},
        columnRules: [],
        primaryKeys: [],
        totalCount: 0,
        firstPage: [],
        pageSize: 1000,
      })
    );
  });

  it("asks the driver for the table's statement with the schema-qualified name and shows it", async () => {
    vi.mocked(GetTableDDL).mockResolvedValue('CREATE TABLE "reporting"."events" (\n  "id" bigint NOT NULL\n);');

    await openDDL("reporting.events");

    await waitFor(() => expect(screen.getByTestId("code-editor").textContent).toContain('"reporting"."events"'));
    expect(vi.mocked(GetTableDDL).mock.calls).toEqual([[1, "appdb", "reporting.events"]]);
    expect(vi.mocked(ExecuteSQL)).not.toHaveBeenCalled();
    expect(vi.mocked(ExecuteSQLPaged)).not.toHaveBeenCalled();
  });

  it("says there is no statement when the driver returns none", async () => {
    vi.mocked(GetTableDDL).mockResolvedValue("");

    await openDDL("users");

    await waitFor(() => expect(screen.getByTestId("code-editor").textContent).toBe("query.ddlEmpty"));
  });

  it("shows the driver's error unchanged in the same place", async () => {
    vi.mocked(GetTableDDL).mockRejectedValue('SQL query failed: permission denied for table "users"');

    await openDDL("users");

    await waitFor(() =>
      expect(screen.getByTestId("code-editor").textContent).toBe(
        'SQL query failed: permission denied for table "users"'
      )
    );
  });
});
