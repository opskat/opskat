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
