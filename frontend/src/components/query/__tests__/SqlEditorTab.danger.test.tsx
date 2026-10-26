import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { SqlEditorTab } from "@/components/query/SqlEditorTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQLPaged } from "../../../../wailsjs/go/query/Query";
import { act } from "@testing-library/react";
import { mounted } from "./fakeSqlEditor";
import { BUILTIN_DRIVERS, provideDrivers } from "@/__tests__/dbDriverFixtures";

vi.mock("@/components/CodeEditor", async () => ({
  CodeEditor: (await import("./FakeCodeEditor")).FakeCodeEditor,
}));

function setup(sql: string) {
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
        tables: { appdb: ["users"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "sql-1", type: "sql", title: "SQL", sql, selectedDb: "appdb" }],
        activeInnerTabId: "sql-1",
        error: null,
      },
    },
  });
}

async function clickExecute(sql: string) {
  setup(sql);
  vi.mocked(ExecuteSQLPaged).mockResolvedValue(JSON.stringify({ columns: ["id"], rows: [{ id: 1 }], total_count: 1 }));
  render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
  // run everything (⇧⌘↵) so every statement is subject to the danger check
  act(() => mounted.current!.runModShift());
}

describe("SqlEditorTab dangerous SQL confirmation", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQLPaged).mockReset();
    provideDrivers(BUILTIN_DRIVERS);
  });

  it.each([
    ["a dangerous statement after a harmless one", "SELECT 1; DROP TABLE x"],
    ["a DELETE behind leading comments", "-- cleanup\n/* old rows */ DELETE FROM t WHERE id = 1"],
    ["an UPDATE without WHERE", "UPDATE users SET name = 'x'"],
  ])("asks for confirmation for %s and does not run until confirmed", async (_n, sql) => {
    await clickExecute(sql);

    expect(await screen.findByText("query.dangerousSqlTitle")).toBeInTheDocument();
    expect(ExecuteSQLPaged).not.toHaveBeenCalled();
  });

  it.each([
    ["an UPDATE with WHERE", "UPDATE users SET name = 'x' WHERE id = 1"],
    ["a plain SELECT", "SELECT * FROM users"],
    ["a DROP that only appears inside a string", "SELECT 'DROP TABLE x; DELETE FROM y'"],
  ])("runs %s directly", async (_n, sql) => {
    await clickExecute(sql);

    await waitFor(() => expect(ExecuteSQLPaged).toHaveBeenCalled());
    expect(screen.queryByText("query.dangerousSqlTitle")).toBeNull();
  });
});
