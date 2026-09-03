import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { DatabaseTree } from "../components/query/DatabaseTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { ExecuteSQL } from "../../wailsjs/go/query/Query";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

function makeDatabaseTab(id = "query-1", driver = "mysql"): void {
  useTabStore.setState({
    tabs: [
      {
        id,
        type: "query",
        label: "test",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "test-db",
          assetIcon: "",
          assetType: "database",
          driver,
          defaultDatabase: "appdb",
        },
      },
    ],
    activeTabId: id,
  });
}

describe("DatabaseTree", () => {
  beforeEach(() => {
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: {},
          loadingTables: {},
          expandedDbs: [],
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
    makeDatabaseTab();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: [] }));
  });

  it("renders PostgreSQL tables grouped by schema and opens qualified table names", () => {
    makeDatabaseTab("query-1", "postgresql");
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: ["adm.ads_audit", "public.users"] },
          loadingTables: {},
          expandedDbs: ["appdb"],
          expandedSchemas: { appdb: ["adm", "public"] },
          loadingDbs: false,
          innerTabs: [],
          activeInnerTabId: null,
          error: null,
        },
      },
      redisStates: {},
      mongoStates: {},
    });

    render(<DatabaseTree tabId="query-1" />);

    expect(screen.getByText("adm")).toBeInTheDocument();
    expect(screen.getByText("ads_audit")).toBeInTheDocument();
    expect(screen.getByText("public")).toBeInTheDocument();
    expect(screen.getByText("users")).toBeInTheDocument();
    expect(screen.queryByText("adm.ads_audit")).not.toBeInTheDocument();

    fireEvent.doubleClick(screen.getByText("ads_audit"));

    expect(useQueryStore.getState().dbStates["query-1"].innerTabs).toEqual([
      { id: "table:appdb.adm.ads_audit", type: "table", database: "appdb", table: "adm.ads_audit" },
    ]);
  });

  it("renders MSSQL tables grouped by schema", () => {
    makeDatabaseTab("query-1", "mssql");
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: ["dbo.users", "sales.orders"] },
          loadingTables: {},
          expandedDbs: ["appdb"],
          expandedSchemas: { appdb: ["dbo", "sales"] },
          loadingDbs: false,
          innerTabs: [],
          activeInnerTabId: null,
          error: null,
        },
      },
      redisStates: {},
      mongoStates: {},
    });

    render(<DatabaseTree tabId="query-1" />);

    expect(screen.getByText("dbo")).toBeInTheDocument();
    expect(screen.getByText("users")).toBeInTheDocument();
    expect(screen.getByText("sales")).toBeInTheDocument();
    expect(screen.getByText("orders")).toBeInTheDocument();
    expect(screen.queryByText("dbo.users")).not.toBeInTheDocument();
  });

  it("filters PostgreSQL schema groups by qualified table names", () => {
    makeDatabaseTab("query-1", "postgresql");
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: ["adm.ads_audit", "public.users"] },
          loadingTables: {},
          expandedDbs: [],
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

    render(<DatabaseTree tabId="query-1" />);

    fireEvent.click(screen.getByTitle("query.filterTables"));
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "adm.ads" } });

    expect(screen.getByText("appdb")).toBeInTheDocument();
    expect(screen.getByText("adm")).toBeInTheDocument();
    expect(screen.getByText("ads_audit")).toBeInTheDocument();
    expect(screen.queryByText("public")).not.toBeInTheDocument();
    expect(screen.queryByText("users")).not.toBeInTheDocument();
  });

  it("does not invent a default schema for unqualified schema-aware table names", () => {
    makeDatabaseTab("query-1", "postgresql");
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: ["users"] },
          loadingTables: {},
          expandedDbs: ["appdb"],
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

    render(<DatabaseTree tabId="query-1" />);

    expect(screen.getByText("users")).toBeInTheDocument();
    expect(screen.queryByText("public")).not.toBeInTheDocument();
  });

  it("does not show a loading spinner for filtered unloaded databases", () => {
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: {},
          loadingTables: {},
          expandedDbs: [],
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

    render(<DatabaseTree tabId="query-1" />);

    fireEvent.click(screen.getByTitle("query.filterTables"));
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "app" } });

    expect(screen.getByText("appdb")).toBeInTheDocument();
    expect(screen.getByText("query.noTables")).toBeInTheDocument();
  });

  it("opens create database dialog from toolbar", async () => {
    render(<DatabaseTree tabId="query-1" />);

    fireEvent.click(screen.getByLabelText("query.createDatabase"));
    expect(screen.getByText("query.createDatabaseDialogTitle")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("query.databaseNamePlaceholder"), {
      target: { value: "reports" },
    });
    fireEvent.change(screen.getByPlaceholderText("query.charsetPlaceholder"), {
      target: { value: "utf8mb4" },
    });
    fireEvent.change(screen.getByPlaceholderText("query.collationPlaceholder"), {
      target: { value: "utf8mb4_0900_ai_ci" },
    });
    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));

    await waitFor(() => {
      expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => {
      expect(ExecuteSQL).toHaveBeenCalledWith(
        1,
        "CREATE DATABASE `reports` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci",
        "appdb"
      );
    });
  });
});

describe("DatabaseTree — table multi-selection", () => {
  beforeEach(() => {
    makeDatabaseTab();
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb", "archivedb"],
          tables: { appdb: ["users", "orders", "logs"], archivedb: ["logs_2024"] },
          loadingTables: {},
          expandedDbs: ["appdb", "archivedb"],
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
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: [] }));
  });

  const node = (table: string) => screen.getByText(table);
  const selectedTables = () =>
    Array.from(document.querySelectorAll('[data-table-node][data-selected="true"]')).map((el) =>
      el.getAttribute("data-table-node")
    );

  it("ctrl-clicking selects tables across databases", () => {
    render(<DatabaseTree tabId="query-1" />);

    fireEvent.click(node("orders"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    expect(selectedTables()).toEqual(["appdb.orders", "archivedb.logs_2024"]);
  });

  it("shift-clicking selects the visible range within a database", () => {
    render(<DatabaseTree tabId="query-1" />);

    fireEvent.click(node("users"));
    fireEvent.click(node("logs"), { shiftKey: true });

    expect(selectedTables()).toEqual(["appdb.users", "appdb.orders", "appdb.logs"]);
  });

  it("a filter change drops tables that are no longer visible", () => {
    render(<DatabaseTree tabId="query-1" />);
    fireEvent.click(node("users"));
    fireEvent.click(node("orders"), { ctrlKey: true });

    fireEvent.click(screen.getByTitle("query.filterTables"));
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "orders" } });

    expect(selectedTables()).toEqual(["appdb.orders"]);
  });

  it("opens every selected table from the context menu", () => {
    render(<DatabaseTree tabId="query-1" />);
    fireEvent.click(node("users"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("logs_2024"));
    fireEvent.click(screen.getByText("query.openTables"));

    expect(useQueryStore.getState().dbStates["query-1"].innerTabs.map((t) => t.id)).toEqual([
      "table:appdb.users",
      "table:archivedb.logs_2024",
    ]);
  });

  it("right-clicking outside the selection resets it to that table", () => {
    render(<DatabaseTree tabId="query-1" />);
    fireEvent.click(node("users"));

    fireEvent.contextMenu(node("orders"));

    expect(selectedTables()).toEqual(["appdb.orders"]);
    expect(screen.queryByText("query.openTables")).not.toBeInTheDocument();
    expect(screen.getByText("query.viewStructure")).toBeInTheDocument();
  });

  it("disables single-table actions while several tables are selected", () => {
    render(<DatabaseTree tabId="query-1" />);
    fireEvent.click(node("users"));
    fireEvent.click(node("orders"), { ctrlKey: true });

    fireEvent.contextMenu(node("orders"));

    expect(screen.getByText("query.viewStructure").closest('[role="menuitem"]')).toHaveAttribute("data-disabled");
    expect(screen.getByText("query.alterTable").closest('[role="menuitem"]')).toHaveAttribute("data-disabled");
  });

  it("drops each selected table against its own database", async () => {
    render(<DatabaseTree tabId="query-1" />);
    fireEvent.click(node("logs"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("logs_2024"));
    fireEvent.click(screen.getByText("query.dropTables"));

    // The confirmation names every affected table, qualified by its database.
    expect(screen.getByText("appdb.logs")).toBeInTheDocument();
    expect(screen.getByText("archivedb.logs_2024")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "query.dropTables" }));

    await waitFor(() => {
      const drops = vi.mocked(ExecuteSQL).mock.calls.filter(([, sql]) => String(sql).startsWith("DROP TABLE"));
      expect(drops).toHaveLength(2);
      expect(drops[0][2]).toBe("appdb");
      expect(drops[1][2]).toBe("archivedb");
    });
  });

  it("keeps dropping the remaining tables when one fails", async () => {
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).includes("logs_2024")) throw new Error("permission denied");
      return JSON.stringify({ rows: [] });
    });
    render(<DatabaseTree tabId="query-1" />);
    fireEvent.click(node("logs"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("logs_2024"));
    fireEvent.click(screen.getByText("query.dropTables"));
    fireEvent.click(screen.getByRole("button", { name: "query.dropTables" }));

    await waitFor(() => {
      const drops = vi.mocked(ExecuteSQL).mock.calls.filter(([, sql]) => String(sql).startsWith("DROP TABLE"));
      expect(drops).toHaveLength(2);
    });
  });
});
