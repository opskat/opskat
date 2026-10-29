import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { DatabasePanel } from "@/components/query/DatabasePanel";
import { useQueryStore, type InnerTab } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity } from "../../../../wailsjs/go/models";
import { notifyCopied } from "@/lib/notify";
import { BUILTIN_DRIVERS, provideDrivers } from "@/__tests__/dbDriverFixtures";

vi.mock("@/components/query/DatabaseTree", () => ({
  DatabaseTree: () => null,
  DriverUnavailableNotice: () => null,
}));
vi.mock("@/components/query/TableDataTab", () => ({ TableDataTab: () => null }));
vi.mock("@/components/query/SqlEditorTab", () => ({ SqlEditorTab: () => null }));
vi.mock("@/components/query/designer/TableDesigner", () => ({
  TableDesigner: (props: { innerTabId: string; database: string; table: string; mode: string }) => (
    <div data-testid="table-designer">{`${props.innerTabId}|${props.database}|${props.table}|${props.mode}`}</div>
  ),
}));
vi.mock("@/components/query/designer/TableCreator", () => ({
  TableCreator: (props: { innerTabId: string; database: string; schema?: string }) => (
    <div data-testid="table-creator">{`${props.innerTabId}|${props.database}|${props.schema ?? ""}`}</div>
  ),
}));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));
const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError } }));

const TAB = "query-1";
const tabs: InnerTab[] = [
  { id: "table:app.users", type: "table", database: "app", table: "users" },
  { id: "sql:1", type: "sql", title: "SQL 1" },
  { id: "table:app.orders", type: "table", database: "app", table: "orders" },
];

function setup(
  active: string | null = "sql:1",
  innerTabs: InnerTab[] = tabs,
  opts: { driver?: string; readOnly?: boolean; expandedDbs?: string[]; defaultDatabase?: string } = {}
) {
  provideDrivers(BUILTIN_DRIVERS);
  const driver = opts.driver ?? "mysql";
  useAssetStore.setState({
    assets: [
      new asset_entity.Asset({
        ID: 1,
        Name: "db",
        Type: "database",
        Config: JSON.stringify({ driver, read_only: !!opts.readOnly }),
      }),
    ],
  });
  useTabStore.setState({
    tabs: [
      {
        id: TAB,
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "db",
          assetIcon: "",
          assetType: "database",
          driver,
          defaultDatabase: opts.defaultDatabase,
        },
      },
    ],
    activeTabId: TAB,
    unsavedTabIds: [],
    pendingCloseTabId: null,
  });
  useQueryStore.setState({
    dbStates: {
      [TAB]: {
        databases: ["app", "logs"],
        tables: {},
        loadingTables: {},
        expandedDbs: opts.expandedDbs ?? [],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs,
        activeInnerTabId: active,
        error: null,
      },
    },
  });
  render(
    <TooltipProvider>
      <DatabasePanel tabId={TAB} />
    </TooltipProvider>
  );
}

const innerIds = () => useQueryStore.getState().dbStates[TAB].innerTabs.map((t) => t.id);
const tabEl = (text: string) => screen.getByText(text).closest("[data-inner-tab]") as HTMLElement;

describe("DatabasePanel inner tab bar", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  it("shows the full name (db.table for tables) in a tooltip on hover", async () => {
    setup();
    await userEvent.hover(tabEl("app.users"));
    expect((await screen.findAllByText("app.users")).length).toBeGreaterThan(1);
  });

  it("closes a tab on middle click", () => {
    setup();
    fireEvent(tabEl("SQL 1"), new MouseEvent("auxclick", { bubbles: true, button: 1 }));
    expect(innerIds()).toEqual(["table:app.users", "table:app.orders"]);
  });

  it("menu: close others keeps only this tab", async () => {
    setup();
    fireEvent.contextMenu(tabEl("SQL 1"));
    fireEvent.click(await screen.findByText("query.innerTab.closeOthers"));
    expect(innerIds()).toEqual(["sql:1"]);
  });

  it("menu: close right keeps the left tabs", async () => {
    setup();
    fireEvent.contextMenu(tabEl("app.users"));
    fireEvent.click(await screen.findByText("query.innerTab.closeRight"));
    expect(innerIds()).toEqual(["table:app.users"]);
  });

  it("menu: close removes only this tab", async () => {
    setup();
    fireEvent.contextMenu(tabEl("app.orders"));
    fireEvent.click(await screen.findByText("query.innerTab.close"));
    expect(innerIds()).toEqual(["table:app.users", "sql:1"]);
  });

  it("menu: copy name reports a clipboard failure instead of confirming", async () => {
    setup();
    vi.mocked(navigator.clipboard.writeText).mockRejectedValueOnce(new Error("clipboard denied"));
    fireEvent.contextMenu(tabEl("app.users"));
    fireEvent.click(await screen.findByText("query.innerTab.copyName"));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Error: clipboard denied"));
    expect(notifyCopied).not.toHaveBeenCalled();
  });

  it("menu: copy name copies db.table for tables and the title for SQL tabs", async () => {
    setup();
    fireEvent.contextMenu(tabEl("app.users"));
    fireEvent.click(await screen.findByText("query.innerTab.copyName"));
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("app.users");
    await act(async () => {});
    expect(notifyCopied).toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: "Escape" });
    fireEvent.contextMenu(tabEl("SQL 1"));
    fireEvent.click(await screen.findByText("query.innerTab.copyName"));
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("SQL 1");
  });

  it("middle click on a tab with unsaved edits hands the decision to that tab instead of closing", () => {
    setup();
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.users", true);
    fireEvent(tabEl("app.users"), new MouseEvent("auxclick", { bubbles: true, button: 1 }));
    expect(innerIds()).toEqual(["table:app.users", "sql:1", "table:app.orders"]);
    expect(useQueryStore.getState().dbStates[TAB].closeRequest?.dirtyIds).toEqual(["table:app.users"]);
    // 单个目标由 TableDataTab 自己的三选项对话框接管,面板不弹批量框。
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("closing others with several edited tabs lists them; cancel keeps every tab", async () => {
    setup();
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.users", true);
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.orders", true);
    fireEvent.contextMenu(tabEl("SQL 1"));
    fireEvent.click(await screen.findByText("query.innerTab.closeOthers"));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("app.users")).toBeInTheDocument();
    expect(within(dialog).getByText("app.orders")).toBeInTheDocument();
    expect(within(dialog).queryByText("query.unsavedEditsSubmit")).toBeNull();

    fireEvent.click(within(dialog).getByText("action.cancel"));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(innerIds()).toEqual(["table:app.users", "sql:1", "table:app.orders"]);
    expect(useQueryStore.getState().dbStates[TAB].closeRequest).toBeUndefined();
  });

  it("closing others with several edited tabs: discard and close closes them", async () => {
    setup();
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.users", true);
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.orders", true);
    fireEvent.contextMenu(tabEl("SQL 1"));
    fireEvent.click(await screen.findByText("query.innerTab.closeOthers"));

    fireEvent.click(within(await screen.findByRole("alertdialog")).getByText("query.unsavedEditsDiscardClose"));
    expect(innerIds()).toEqual(["sql:1"]);
  });

  it("closing the database tab with several edited tabs asks once and closes it on discard", async () => {
    setup();
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.users", true);
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.orders", true);
    act(() => useTabStore.getState().closeTab(TAB));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("app.users")).toBeInTheDocument();
    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual([TAB]);

    fireEvent.click(within(dialog).getByText("query.unsavedEditsDiscardClose"));
    expect(useTabStore.getState().tabs).toEqual([]);
  });

  it("closing the database tab with one edited tab hands the decision to that tab", () => {
    setup();
    useQueryStore.getState().setInnerTabDirty(TAB, "table:app.orders", true);
    act(() => useTabStore.getState().closeTab(TAB));
    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual([TAB]);
    expect(useQueryStore.getState().dbStates[TAB].closeRequest).toMatchObject({
      dirtyIds: ["table:app.orders"],
      closeOuter: true,
    });
    expect(useQueryStore.getState().dbStates[TAB].activeInnerTabId).toBe("table:app.orders");
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("menu: reveal in tree is offered for table tabs only and reveals that table", async () => {
    setup();
    const reveal = vi.spyOn(useQueryStore.getState(), "revealTable");
    fireEvent.contextMenu(tabEl("SQL 1"));
    await screen.findByText("query.innerTab.copyName");
    expect(screen.queryByText("query.innerTab.reveal")).toBeNull();
    fireEvent.keyDown(document.body, { key: "Escape" });
    fireEvent.contextMenu(tabEl("app.orders"));
    fireEvent.click(await screen.findByText("query.innerTab.reveal"));
    expect(reveal).toHaveBeenCalledWith(TAB, "app", "orders");
  });
});

describe("DatabasePanel 设计器标签", () => {
  const designer: InnerTab = {
    id: "designer:app.orders",
    type: "designer",
    mode: "view",
    database: "app",
    table: "orders",
  };

  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  it("只读查看的标签以「结构」为题,内容是这张表的设计器", () => {
    setup(designer.id, [...tabs, designer]);
    expect(tabEl("query.designer.tabTitle.view")).toBeInTheDocument();
    expect(screen.getByTestId("table-designer")).toHaveTextContent("designer:app.orders|app|orders|view");
  });

  it("设计表的标签以「设计」为题,设计器以设计模式打开", () => {
    const design: InnerTab = { ...designer, mode: "design" };
    setup(design.id, [...tabs, design]);
    expect(tabEl("query.designer.tabTitle.design")).toBeInTheDocument();
    expect(screen.getByTestId("table-designer")).toHaveTextContent("designer:app.orders|app|orders|design");
  });

  it("设计表的标签在资产是只读连接时(如恢复的标签、之后开启了只读连接)以「结构」为题", async () => {
    const design: InnerTab = { ...designer, mode: "design" };
    setup(design.id, [...tabs, design], { readOnly: true });
    expect(await screen.findByText("query.designer.tabTitle.view")).toBeInTheDocument();
    expect(screen.queryByText("query.designer.tabTitle.design")).not.toBeInTheDocument();
  });

  it("有未保存改动的标签显示圆点", () => {
    const design: InnerTab = { ...designer, mode: "design" };
    setup(design.id, [...tabs, design]);
    expect(within(tabEl("query.designer.tabTitle.design")).queryByTestId("inner-tab-dirty")).toBeNull();
    act(() => useQueryStore.getState().setInnerTabDirty(TAB, design.id, true));
    expect(within(tabEl("query.designer.tabTitle.design")).getByTestId("inner-tab-dirty")).toBeInTheDocument();
    expect(within(tabEl("app.users")).queryByTestId("inner-tab-dirty")).toBeNull();
  });

  it("批量关闭多个有改动的标签时,批量确认里按标题列出设计器", () => {
    const design: InnerTab = { ...designer, mode: "design" };
    setup(design.id, [...tabs, design]);
    act(() => {
      useQueryStore.getState().setInnerTabDirty(TAB, design.id, true);
      useQueryStore.getState().setInnerTabDirty(TAB, "table:app.users", true);
      useQueryStore.getState().closeOtherInnerTabs(TAB, "sql:1");
    });
    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByText("query.designer.tabTitle.design")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "query.unsavedEditsDiscardClose" }));
    expect(innerIds()).toEqual(["sql:1"]);
  });

  it("菜单:复制名称给出 db.table,并能在左侧树中定位", async () => {
    setup(designer.id, [...tabs, designer]);
    const reveal = vi.spyOn(useQueryStore.getState(), "revealTable");
    fireEvent.contextMenu(tabEl("query.designer.tabTitle.view"));
    fireEvent.click(await screen.findByText("query.innerTab.copyName"));
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("app.orders");
    await act(async () => {});
    fireEvent.keyDown(document.body, { key: "Escape" });
    fireEvent.contextMenu(tabEl("query.designer.tabTitle.view"));
    fireEvent.click(await screen.findByText("query.innerTab.reveal"));
    expect(reveal).toHaveBeenCalledWith(TAB, "app", "orders");
  });
});

describe("DatabasePanel 新建表", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("新建表标签以「新建表 · 库」为题,内容是新建表的设计器;复制名称给出库,没有「在树中定位」", async () => {
    const create: InnerTab = { id: "create:x", type: "designer", mode: "create", database: "app", schema: "sales" };
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    setup(create.id, [...tabs, create]);
    expect(tabEl("query.designer.tabTitle.create")).toBeInTheDocument();
    expect(screen.getByTestId("table-creator")).toHaveTextContent("create:x|app|sales");
    fireEvent.contextMenu(tabEl("query.designer.tabTitle.create"));
    expect(screen.queryByText("query.innerTab.reveal")).not.toBeInTheDocument();
    fireEvent.click(await screen.findByText("query.innerTab.copyName"));
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("app.sales");
  });

  it("没有内层标签时空白区给「新建查询 / 新建表」:新建表建在第一个展开的库", async () => {
    setup(null, [], { expandedDbs: ["logs"], defaultDatabase: "app" });
    await screen.findByTestId("empty-new-table");
    fireEvent.click(screen.getByTestId("empty-new-table"));
    const state = useQueryStore.getState().dbStates[TAB];
    expect(state.innerTabs).toEqual([
      { id: expect.stringMatching(/^create:/), type: "designer", mode: "create", database: "logs" },
    ]);
    expect(state.activeInnerTabId).toBe(state.innerTabs[0].id);
  });

  it("没有展开的库时新建表建在资产的默认库;「新建查询」打开 SQL 标签", async () => {
    setup(null, [], { defaultDatabase: "app" });
    fireEvent.click(await screen.findByTestId("empty-new-table"));
    expect(useQueryStore.getState().dbStates[TAB].innerTabs[0]).toMatchObject({ mode: "create", database: "app" });
    act(() => useQueryStore.getState().forceCloseInnerTabs(TAB, innerIds()));
    fireEvent.click(await screen.findByTestId("empty-new-sql"));
    expect(useQueryStore.getState().dbStates[TAB].innerTabs[0]).toMatchObject({ type: "sql" });
  });

  it("只读连接或驱动没声明建表:空白区只有「新建查询」", async () => {
    setup(null, [], { readOnly: true, defaultDatabase: "app" });
    await screen.findByTestId("empty-new-sql");
    expect(screen.queryByTestId("empty-new-table")).not.toBeInTheDocument();
  });
});
