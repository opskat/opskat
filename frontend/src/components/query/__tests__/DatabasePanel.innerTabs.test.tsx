import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { DatabasePanel } from "@/components/query/DatabasePanel";
import { useQueryStore, type InnerTab } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { notifyCopied } from "@/lib/notify";
import { BUILTIN_DRIVERS, provideDrivers } from "@/__tests__/dbDriverFixtures";

vi.mock("@/components/query/DatabaseTree", () => ({
  DatabaseTree: () => null,
  DriverUnavailableNotice: () => null,
}));
vi.mock("@/components/query/TableDataTab", () => ({ TableDataTab: () => null }));
vi.mock("@/components/query/SqlEditorTab", () => ({ SqlEditorTab: () => null }));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));
const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError } }));

const TAB = "query-1";
const tabs: InnerTab[] = [
  { id: "table:app.users", type: "table", database: "app", table: "users" },
  { id: "sql:1", type: "sql", title: "SQL 1" },
  { id: "table:app.orders", type: "table", database: "app", table: "orders" },
];

function setup(active = "sql:1") {
  provideDrivers(BUILTIN_DRIVERS);
  useTabStore.setState({
    tabs: [
      {
        id: TAB,
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 1, assetName: "db", assetIcon: "", assetType: "database", driver: "mysql" },
      },
    ],
    activeTabId: TAB,
    unsavedTabIds: [],
    pendingCloseTabId: null,
  });
  useQueryStore.setState({
    dbStates: {
      [TAB]: {
        databases: [],
        tables: {},
        loadingTables: {},
        expandedDbs: [],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: tabs,
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
