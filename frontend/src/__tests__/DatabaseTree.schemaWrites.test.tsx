import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { DatabaseTree } from "@/components/query/DatabaseTree";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import {
  ExecuteSQL,
  GetCreateDatabaseStatements,
  GetDropTableStatements,
  GetTruncateTableStatements,
} from "../../wailsjs/go/query/Query";
import { dbdriver } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, mockSchemaStatements, provideDrivers } from "./dbDriverFixtures";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

// 建库对话框的选项字段与默认值、清空与删表的语句,都由驱动给出;树与对话框不认驱动名。

function setup(driver: string) {
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
          defaultDatabase: "appdb",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["users", "logs"] },
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
}

async function renderTree() {
  await act(async () => {
    render(<DatabaseTree tabId="query-1" />);
  });
}

const driverWithOptions = (options: { key: string; default: string }[]) =>
  new dbdriver.Descriptor({
    ...fakeDriverWith(["create_database", "truncate_table", "drop_table"]),
    createDatabaseOptions: options,
  });

describe("DatabaseTree — create database options", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: [] }));
    vi.mocked(GetCreateDatabaseStatements).mockReset();
    vi.mocked(toast.error).mockReset();
    mockSchemaStatements();
  });

  async function openDialog(driver: string) {
    setup(driver);
    await renderTree();
    fireEvent.click(screen.getByLabelText("query.createDatabase"));
    fireEvent.change(screen.getByPlaceholderText("query.databaseNamePlaceholder"), { target: { value: "reports" } });
  }

  it("shows only the option fields the driver declares: SQL Server has a collation and no charset", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    await openDialog("mssql");

    expect(screen.queryByPlaceholderText("query.charsetPlaceholder")).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText("query.collationPlaceholder")).toHaveValue("");

    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));
    await waitFor(() => expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument());
    expect(GetCreateDatabaseStatements).toHaveBeenCalledWith(1, "appdb", {
      name: "reports",
      options: { collation: "" },
    });
  });

  it("uses the defaults the driver declares", async () => {
    provideDrivers([driverWithOptions([{ key: "charset", default: "LATIN1" }])]);
    await openDialog("fakedb");

    expect(screen.getByPlaceholderText("query.charsetPlaceholder")).toHaveValue("LATIN1");
    expect(screen.queryByPlaceholderText("query.collationPlaceholder")).not.toBeInTheDocument();
  });

  it("keeps refusing option values with unsafe characters, without asking the driver", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    await openDialog("mysql");
    fireEvent.change(screen.getByPlaceholderText("query.charsetPlaceholder"), { target: { value: "utf8; DROP" } });
    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("query.createDatabaseOptionInvalid"));
    expect(GetCreateDatabaseStatements).not.toHaveBeenCalled();
    expect(screen.queryByText("query.sqlPreviewTitle")).not.toBeInTheDocument();
  });

  it("shows the error and opens no preview when the driver cannot write the statement", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    vi.mocked(GetCreateDatabaseStatements).mockRejectedValue(new Error("unsupported"));
    await openDialog("mysql");
    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: unsupported"));
    expect(screen.queryByText("query.sqlPreviewTitle")).not.toBeInTheDocument();
    expect(ExecuteSQL).not.toHaveBeenCalled();
  });
});

describe("DatabaseTree — drop and truncate", () => {
  beforeEach(() => {
    provideDrivers([...BUILTIN_DRIVERS, driverWithOptions([])]);
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: [] }));
    vi.mocked(toast.error).mockReset();
    mockSchemaStatements();
  });

  it("executes the statement the driver wrote for truncate, for a driver without TRUNCATE", async () => {
    setup("sqlite");
    vi.mocked(GetTruncateTableStatements).mockImplementation(async (_id, database, table) => [
      `DELETE FROM ${database}.${table}`,
    ]);
    await renderTree();

    fireEvent.contextMenu(screen.getByText("users"));
    fireEvent.click(screen.getByText("query.truncateTable"));
    fireEvent.click(screen.getByRole("button", { name: "query.truncateTable" }));

    await waitFor(() => expect(ExecuteSQL).toHaveBeenCalledWith(1, "DELETE FROM appdb.users", "appdb"));
    expect(GetTruncateTableStatements).toHaveBeenCalledWith(1, "appdb", "users");
  });

  it("executes nothing for a table whose statement could not be written, and reports the error", async () => {
    setup("fakedb");
    vi.mocked(GetDropTableStatements).mockImplementation(async (_id, database, table) => {
      if (table === "logs") throw new Error("unsupported");
      return [`DROP ${database}.${table}`];
    });
    await renderTree();

    fireEvent.click(screen.getByText("users"));
    fireEvent.click(screen.getByText("logs"), { ctrlKey: true });
    fireEvent.contextMenu(screen.getByText("logs"));
    fireEvent.click(screen.getByText("query.dropTables"));
    fireEvent.click(screen.getByRole("button", { name: "query.dropTables" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("appdb.logs: Error: unsupported"))
    );
    expect(vi.mocked(ExecuteSQL).mock.calls).toEqual([[1, "DROP appdb.users", "appdb"]]);
  });
});
