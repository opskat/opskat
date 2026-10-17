import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { DatabaseTree } from "../components/query/DatabaseTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { ExecuteSQL, GetCreateDatabaseStatements, GetStarterQuery, ListTables } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, FAKE_DRIVER, mockSchemaStatements, provideDrivers } from "./dbDriverFixtures";

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

// 入口与分组由资产驱动的描述决定;描述异步到达,渲染后等它落地。
async function renderTree() {
  await act(async () => {
    render(<DatabaseTree tabId="query-1" />);
  });
}

describe("DatabaseTree", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
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

  it("renders PostgreSQL tables grouped by schema and opens qualified table names", async () => {
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

    await renderTree();

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

  it("renders MSSQL tables grouped by schema", async () => {
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

    await renderTree();

    expect(screen.getByText("dbo")).toBeInTheDocument();
    expect(screen.getByText("users")).toBeInTheDocument();
    expect(screen.getByText("sales")).toBeInTheDocument();
    expect(screen.getByText("orders")).toBeInTheDocument();
    expect(screen.queryByText("dbo.users")).not.toBeInTheDocument();
  });

  it("filters PostgreSQL schema groups by qualified table names", async () => {
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

    await renderTree();

    fireEvent.click(screen.getByTitle("query.filterTables"));
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "adm.ads" } });

    expect(screen.getByText("appdb")).toBeInTheDocument();
    expect(screen.getByText("adm")).toBeInTheDocument();
    expect(screen.getByText("ads_audit")).toBeInTheDocument();
    expect(screen.queryByText("public")).not.toBeInTheDocument();
    expect(screen.queryByText("users")).not.toBeInTheDocument();
  });

  it("does not invent a default schema for unqualified schema-aware table names", async () => {
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

    await renderTree();

    expect(screen.getByText("users")).toBeInTheDocument();
    expect(screen.queryByText("public")).not.toBeInTheDocument();
  });

  it("does not show a loading spinner for filtered unloaded databases", async () => {
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

    await renderTree();

    fireEvent.click(screen.getByTitle("query.filterTables"));
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "app" } });

    expect(screen.getByText("appdb")).toBeInTheDocument();
    expect(screen.getByText("query.noTables")).toBeInTheDocument();
  });

  it("creates a database with the statement the driver wrote, from the driver's declared option fields", async () => {
    mockSchemaStatements();
    await renderTree();

    fireEvent.click(screen.getByLabelText("query.createDatabase"));
    expect(screen.getByText("query.createDatabaseDialogTitle")).toBeInTheDocument();

    // The fields and their defaults come from the descriptor.
    expect(screen.getByPlaceholderText("query.charsetPlaceholder")).toHaveValue("utf8mb4");
    expect(screen.getByPlaceholderText("query.collationPlaceholder")).toHaveValue("utf8mb4_0900_ai_ci");

    fireEvent.change(screen.getByPlaceholderText("query.databaseNamePlaceholder"), {
      target: { value: "reports" },
    });
    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));

    await waitFor(() => {
      expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument();
    });
    expect(GetCreateDatabaseStatements).toHaveBeenCalledWith(1, "appdb", {
      name: "reports",
      options: { charset: "utf8mb4", collation: "utf8mb4_0900_ai_ci" },
    });
    expect(screen.getByTestId("code-editor")).toHaveTextContent(
      'CREATE DATABASE reports {"charset":"utf8mb4","collation":"utf8mb4_0900_ai_ci"}'
    );

    fireEvent.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => {
      expect(ExecuteSQL).toHaveBeenCalledWith(
        1,
        'CREATE DATABASE reports {"charset":"utf8mb4","collation":"utf8mb4_0900_ai_ci"}',
        "appdb"
      );
    });
  });
});

describe("DatabaseTree — table multi-selection", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
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
    mockSchemaStatements();
  });

  const node = (table: string) => screen.getByText(table);
  const selectedTables = () =>
    Array.from(document.querySelectorAll('[data-table-node][data-selected="true"]')).map((el) =>
      el.getAttribute("data-table-node")
    );

  it("ctrl-clicking selects tables across databases", async () => {
    await renderTree();

    fireEvent.click(node("orders"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    expect(selectedTables()).toEqual(["appdb.orders", "archivedb.logs_2024"]);
  });

  it("shift-clicking selects the visible range within a database", async () => {
    await renderTree();

    fireEvent.click(node("users"));
    fireEvent.click(node("logs"), { shiftKey: true });

    expect(selectedTables()).toEqual(["appdb.users", "appdb.orders", "appdb.logs"]);
  });

  it("a filter change drops tables that are no longer visible", async () => {
    await renderTree();
    fireEvent.click(node("users"));
    fireEvent.click(node("orders"), { ctrlKey: true });

    fireEvent.click(screen.getByTitle("query.filterTables"));
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "orders" } });

    expect(selectedTables()).toEqual(["appdb.orders"]);

    // Dropped means dropped: clearing the filter must not resurrect `users`, or a later
    // batch action would silently cover a table the user believes they deselected.
    fireEvent.change(screen.getByPlaceholderText("query.filterTables"), { target: { value: "" } });

    expect(selectedTables()).toEqual(["appdb.orders"]);
  });

  it("collapsing a database drops its tables from the selection", async () => {
    await renderTree();
    fireEvent.click(node("users"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.click(node("appdb"));
    fireEvent.click(node("appdb"));

    expect(selectedTables()).toEqual(["archivedb.logs_2024"]);
  });

  it("refreshing a database's tables drops its tables from the selection", async () => {
    // The reload brings the very same tables back, so a selection that merely hid
    // itself while loading would reappear here.
    vi.mocked(ListTables).mockResolvedValue(["users", "orders", "logs"]);
    await renderTree();
    fireEvent.click(node("users"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("appdb"));
    fireEvent.click(screen.getByText("query.refreshTables"));

    await waitFor(() => expect(node("users")).toBeTruthy());
    expect(selectedTables()).toEqual(["archivedb.logs_2024"]);
  });

  it("opens every selected table from the context menu", async () => {
    await renderTree();
    fireEvent.click(node("users"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("logs_2024"));
    fireEvent.click(screen.getByText("query.openTables"));

    expect(useQueryStore.getState().dbStates["query-1"].innerTabs.map((t) => t.id)).toEqual([
      "table:appdb.users",
      "table:archivedb.logs_2024",
    ]);
  });

  it("right-clicking outside the selection resets it to that table", async () => {
    await renderTree();
    fireEvent.click(node("users"));

    fireEvent.contextMenu(node("orders"));

    expect(selectedTables()).toEqual(["appdb.orders"]);
    expect(screen.queryByText("query.openTables")).not.toBeInTheDocument();
    expect(screen.getByText("query.viewStructure")).toBeInTheDocument();
  });

  it("disables single-table actions while several tables are selected", async () => {
    await renderTree();
    fireEvent.click(node("users"));
    fireEvent.click(node("orders"), { ctrlKey: true });

    fireEvent.contextMenu(node("orders"));

    expect(screen.getByText("query.viewStructure").closest('[role="menuitem"]')).toHaveAttribute("data-disabled");
    expect(screen.getByText("query.alterTable").closest('[role="menuitem"]')).toHaveAttribute("data-disabled");
  });

  it("drops each selected table against its own database", async () => {
    await renderTree();
    fireEvent.click(node("logs"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("logs_2024"));
    fireEvent.click(screen.getByText("query.dropTables"));

    // The confirmation names every affected table, qualified by its database.
    expect(screen.getByText("appdb.logs")).toBeInTheDocument();
    expect(screen.getByText("archivedb.logs_2024")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "query.dropTables" }));

    await waitFor(() => {
      const drops = vi.mocked(ExecuteSQL).mock.calls.filter(([, sql]) => String(sql).startsWith("DROP "));
      expect(drops).toEqual([
        [1, "DROP appdb.logs", "appdb"],
        [1, "DROP archivedb.logs_2024", "archivedb"],
      ]);
    });
  });

  it("keeps dropping the remaining tables when one fails", async () => {
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).includes("logs_2024")) throw new Error("permission denied");
      return JSON.stringify({ rows: [] });
    });
    await renderTree();
    fireEvent.click(node("logs"));
    fireEvent.click(node("logs_2024"), { ctrlKey: true });

    fireEvent.contextMenu(node("logs_2024"));
    fireEvent.click(screen.getByText("query.dropTables"));
    fireEvent.click(screen.getByRole("button", { name: "query.dropTables" }));

    await waitFor(() => {
      const drops = vi.mocked(ExecuteSQL).mock.calls.filter(([, sql]) => String(sql).startsWith("DROP "));
      expect(drops).toHaveLength(2);
    });
  });
});

describe("DatabaseTree — schema-aware selection", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    makeDatabaseTab("query-1", "postgresql");
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: ["alpha.a1", "beta.b1", "gamma.g1"] },
          loadingTables: {},
          expandedDbs: ["appdb"],
          expandedSchemas: { appdb: ["alpha", "beta", "gamma"] },
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

  const selectedTables = () =>
    Array.from(document.querySelectorAll('[data-table-node][data-selected="true"]')).map((el) =>
      el.getAttribute("data-table-node")
    );

  it("collapsing a schema drops its tables from the selection", async () => {
    await renderTree();
    fireEvent.click(screen.getByText("a1"));
    fireEvent.click(screen.getByText("b1"), { ctrlKey: true });

    fireEvent.click(screen.getByText("beta"));
    expect(selectedTables()).toEqual(["appdb.alpha.a1"]);

    // Re-expanding must not resurrect it, or a later batch drop would silently cover
    // a table the user believes they put away.
    fireEvent.click(screen.getByText("beta"));
    expect(selectedTables()).toEqual(["appdb.alpha.a1"]);
  });

  it("a shift range skips tables hidden inside a collapsed schema", async () => {
    await renderTree();
    fireEvent.click(screen.getByText("beta"));

    fireEvent.click(screen.getByText("a1"));
    fireEvent.click(screen.getByText("g1"), { shiftKey: true });
    expect(selectedTables()).toEqual(["appdb.alpha.a1", "appdb.gamma.g1"]);

    // The batch action is the thing that matters: a hidden table caught by the range
    // would be acted on without ever appearing on screen.
    fireEvent.contextMenu(screen.getByText("g1"));
    fireEvent.click(screen.getByText("query.openTables"));

    expect(useQueryStore.getState().dbStates["query-1"].innerTabs.map((tab) => tab.id)).toEqual([
      "table:appdb.alpha.a1",
      "table:appdb.gamma.g1",
    ]);
  });
});

describe("DatabaseTree — starter query", () => {
  beforeEach(() => {
    provideDrivers([FAKE_DRIVER]);
    makeDatabaseTab("query-1", "fakedb");
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
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(GetStarterQuery).mockReset();
  });

  it("opens the SQL editor with the statement the driver wrote, asked for with a structured request", async () => {
    vi.mocked(GetStarterQuery).mockResolvedValue("FAKE SELECT FIRST 100 FROM appdb.users");
    const openSqlTab = vi.fn();
    useQueryStore.setState({ openSqlTab });
    await renderTree();

    fireEvent.contextMenu(screen.getByText("users"));
    fireEvent.click(screen.getByText("query.newSql"));

    await waitFor(() =>
      expect(openSqlTab).toHaveBeenCalledWith("query-1", "appdb", "FAKE SELECT FIRST 100 FROM appdb.users")
    );
    expect(vi.mocked(GetStarterQuery).mock.calls).toEqual([[1, "appdb", "users", 100]]);
    expect(ExecuteSQL).not.toHaveBeenCalled();
  });

  it("opens no editor and reports the error when the driver cannot write the statement", async () => {
    vi.mocked(GetStarterQuery).mockRejectedValue("operation not supported by database driver");
    const openSqlTab = vi.fn();
    useQueryStore.setState({ openSqlTab });
    await renderTree();

    fireEvent.contextMenu(screen.getByText("users"));
    fireEvent.click(screen.getByText("query.newSql"));

    await waitFor(() => expect(GetStarterQuery).toHaveBeenCalled());
    expect(openSqlTab).not.toHaveBeenCalled();
  });
});

describe("DatabaseTree — tables group and reveal", () => {
  const seed = (tables: string[], expandedDbs: string[], expandedSchemas: Record<string, string[]> = {}) =>
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: ["appdb"],
          tables: { appdb: tables },
          loadingTables: {},
          expandedDbs,
          expandedSchemas,
          loadingDbs: false,
          innerTabs: [],
          activeInnerTabId: null,
          error: null,
        },
      },
      redisStates: {},
      mongoStates: {},
    });

  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("collapses the tables group on the first click and expands it on the next", async () => {
    makeDatabaseTab("query-1", "mysql");
    seed(["users", "orders"], ["appdb"]);
    await renderTree();
    expect(screen.getByText("users")).toBeInTheDocument();

    fireEvent.click(screen.getByText(/query\.objTables/));
    expect(screen.queryByText("users")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText(/query\.objTables/));
    expect(screen.getByText("users")).toBeInTheDocument();
  });

  it("reveals a table in a collapsed database: expands it, selects the table and scrolls to it", async () => {
    makeDatabaseTab("query-1", "mysql");
    seed(["users", "orders"], []);
    await renderTree();
    expect(screen.queryByText("orders")).not.toBeInTheDocument();

    act(() => useQueryStore.getState().revealTable("query-1", "appdb", "orders"));

    const node = await waitFor(() => {
      const el = document.querySelector('[data-table-node="appdb.orders"]');
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    expect(node.getAttribute("data-selected")).toBe("true");
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
    expect(vi.mocked(Element.prototype.scrollIntoView).mock.contexts.at(-1)).toBe(node);
  });

  it("reveals the same table again after the user scrolled or clicked elsewhere", async () => {
    makeDatabaseTab("query-1", "mysql");
    seed(["users", "orders"], ["appdb"]);
    await renderTree();
    act(() => useQueryStore.getState().revealTable("query-1", "appdb", "orders"));
    await waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByText("users"));
    expect(document.querySelector('[data-table-node="appdb.orders"]')!.getAttribute("data-selected")).not.toBe("true");

    act(() => useQueryStore.getState().revealTable("query-1", "appdb", "orders"));

    await waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalledTimes(2));
    expect(document.querySelector('[data-table-node="appdb.orders"]')!.getAttribute("data-selected")).toBe("true");
  });

  it("reveals through a collapsed tables group", async () => {
    makeDatabaseTab("query-1", "mysql");
    seed(["users", "orders"], ["appdb"]);
    await renderTree();
    fireEvent.click(screen.getByText(/query\.objTables/));
    expect(screen.queryByText("orders")).not.toBeInTheDocument();

    act(() => useQueryStore.getState().revealTable("query-1", "appdb", "orders"));

    await waitFor(() =>
      expect(document.querySelector('[data-table-node="appdb.orders"]')?.getAttribute("data-selected")).toBe("true")
    );
  });

  it("reveals a schema-qualified table by expanding its schema", async () => {
    makeDatabaseTab("query-1", "postgresql");
    seed(["alpha.a1", "beta.b1"], ["appdb"], { appdb: ["alpha"] });
    await renderTree();
    expect(document.querySelector('[data-table-node="appdb.beta.b1"]')).toBeNull();

    act(() => useQueryStore.getState().revealTable("query-1", "appdb", "beta.b1"));

    await waitFor(() =>
      expect(document.querySelector('[data-table-node="appdb.beta.b1"]')?.getAttribute("data-selected")).toBe("true")
    );
  });

  it("waits for tables that are not loaded yet, then reveals", async () => {
    makeDatabaseTab("query-1", "mysql");
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
    vi.mocked(ListTables).mockResolvedValueOnce(["users", "orders"]);
    await renderTree();

    await act(async () => useQueryStore.getState().revealTable("query-1", "appdb", "orders"));

    await waitFor(() =>
      expect(document.querySelector('[data-table-node="appdb.orders"]')?.getAttribute("data-selected")).toBe("true")
    );
  });
});
