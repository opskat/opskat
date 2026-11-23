import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { DatabaseTree } from "../components/query/DatabaseTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { useAssetStore } from "../stores/assetStore";
import { ListDatabaseObjects, ListTables, SelectSQLFile, StartRunSQLFile } from "../../wailsjs/go/query/Query";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { asset_entity, query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";
import type { RunSqlFileEvent } from "@/components/query/RunSqlFileDialog";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

function openTab(driver: string) {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "demo-db",
          assetIcon: "",
          assetType: "database",
          driver,
          defaultDatabase: "main",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["main"],
        tables: { main: ["users"] },
        loadingTables: {},
        expandedDbs: ["main"],
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

async function renderTree(driver: string) {
  openTab(driver);
  render(<DatabaseTree tabId="query-1" />);
  await screen.findByText(/^query\.objTables/);
}

/** 数据库节点右键菜单里的「运行 SQL 文件…」;没有时为 null。 */
function runSqlFileItem(): HTMLElement | null {
  fireEvent.contextMenu(screen.getByText("main"));
  return screen.queryByText("query.runSqlFile")?.closest<HTMLElement>("[role=menuitem]") ?? null;
}

beforeEach(() => {
  useAssetStore.setState({ assets: [] });
  vi.mocked(ListDatabaseObjects).mockReset();
  vi.mocked(ListDatabaseObjects).mockResolvedValue(new query_svc.DatabaseObjects({}));
  vi.mocked(ListTables).mockReset();
  vi.mocked(ListTables).mockResolvedValue(["users", "orders"]);
});

describe("DatabaseTree 的「运行 SQL 文件…」", () => {
  it("只在驱动声明了 run_sql_file 时出现在库节点上", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    await renderTree("sqlite");
    expect(runSqlFileItem()).not.toBeNull();

    cleanup();
    provideDrivers([fakeDriverWith([])]);
    await renderTree("fakedb");
    expect(runSqlFileItem()).toBeNull();
  });

  it("资产设为只读连接时置灰,悬停说明原因", async () => {
    provideDrivers([fakeDriverWith(["run_sql_file"])]);
    useAssetStore.setState({
      assets: [
        new asset_entity.Asset({ ID: 1, Name: "db", Type: "database", Config: JSON.stringify({ read_only: true }) }),
      ],
    });
    await renderTree("fakedb");

    const item = runSqlFileItem();
    expect(item).toHaveAttribute("data-disabled");
    expect(item?.closest("[title]")).toHaveAttribute("title", "query.runSqlFileReadOnlyHint");
    fireEvent.click(item!);
    expect(screen.queryByText("query.runSqlFileTitle")).not.toBeInTheDocument();
  });

  it("打开对话框,在这个库上运行;运行结束后刷新这个库的表与对象", async () => {
    let listener: ((event: RunSqlFileEvent) => void) | undefined;
    vi.mocked(EventsOn).mockImplementation((_name: string, callback: (...data: never[]) => void) => {
      listener = callback as unknown as (event: RunSqlFileEvent) => void;
      return () => {};
    });
    vi.mocked(SelectSQLFile).mockResolvedValue({ path: "/tmp/seed.sql", size: 10 } as never);
    vi.mocked(StartRunSQLFile).mockResolvedValue("run-1");
    provideDrivers([fakeDriverWith(["run_sql_file"])]);
    await renderTree("fakedb");

    fireEvent.click(runSqlFileItem()!);
    expect(await screen.findByText("query.runSqlFileTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /query\.runSqlFileChoose/ }));
    await screen.findByText("/tmp/seed.sql");
    fireEvent.click(screen.getByRole("button", { name: /query\.runSqlFileStart/ }));
    await waitFor(() =>
      expect(StartRunSQLFile).toHaveBeenCalledWith(1, "main", { path: "/tmp/seed.sql", continueOnError: false })
    );
    vi.mocked(ListTables).mockClear();
    vi.mocked(ListDatabaseObjects).mockClear();

    act(() =>
      listener!({
        runId: "run-1",
        status: "done",
        progress: { succeeded: 1, failed: 0, bytesRead: 10, totalBytes: 10 },
        elapsedMs: 1,
      })
    );
    await waitFor(() => expect(ListTables).toHaveBeenCalledWith(1, "main"));
    await waitFor(() => expect(ListDatabaseObjects).toHaveBeenCalledWith(1, "main"));
  });
});
