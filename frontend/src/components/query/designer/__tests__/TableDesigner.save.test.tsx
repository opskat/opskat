import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, renderHook, screen, waitFor, within } from "@testing-library/react";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { DEFAULT_SHORTCUTS, isMac, useShortcutStore, type ShortcutBinding } from "@/stores/shortcutStore";
import { useKeyboardShortcuts } from "@/hooks/useKeyboardShortcuts";
import { notifySuccess } from "@/lib/notify";
import { dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import {
  ExecuteSchemaStatements,
  GetAlterTableModel,
  GetAlterTableStatements,
  GetTableMetadata,
  ListTables,
} from "../../../../../wailsjs/go/query/Query";
import { fakeAlterDriver } from "@/__tests__/dbDriverFixtures";
import {
  INNER_ID,
  TAB_ID,
  addField,
  legacyModel,
  mysqlModel,
  openMysql,
  openWith,
  rowOf,
  saveButton,
} from "./designerKit";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

// 保存:先确认(列出驱动给的全部语句,删除列 / 索引时红色警告),确认后整批交给 ExecuteSchemaStatements;
// 整批回滚、逐条停在中途、全部成功、根本没能执行四种结局;⌘S;脏标记与关闭拦截。

const db = () => useQueryStore.getState().dbStates[TAB_ID];

function nameInput(column: string) {
  return within(rowOf(column)).getByRole("textbox", { name: "query.designer.column.name" });
}

/** 标记删除的行名称是文本(删除线),不是输入框。 */
function deletedRow(name: string): HTMLElement {
  return screen.getByText(name).closest('[role="row"]') as HTMLElement;
}

function renameColumn(from: string, to: string) {
  fireEvent.change(nameInput(from), { target: { value: to } });
}

function deleteColumn(name: string) {
  fireEvent.click(within(rowOf(name)).getByRole("button", { name: "query.designer.deleteColumn" }));
}

function renameTable(name: string) {
  fireEvent.change(screen.getByRole("textbox", { name: "query.designer.tableName" }), { target: { value: name } });
}

async function clickSave() {
  await waitFor(() => expect(saveButton()).toBeEnabled());
  fireEvent.click(saveButton());
  return screen.findByRole("alertdialog");
}

function statementsIn(dialog: HTMLElement) {
  return within(dialog).getAllByTestId("save-statement");
}

function batchResult(
  statuses: { statement: string; status: string; error?: string }[],
  opts: { atomic?: boolean; rolledBack?: boolean } = {}
) {
  return query_svc.SchemaBatchResult.createFrom({
    atomic: !!opts.atomic,
    rolledBack: !!opts.rolledBack,
    statements: statuses,
  });
}

function succeedAll() {
  vi.mocked(ExecuteSchemaStatements).mockImplementation(async (_a, _d, statements) =>
    batchResult(statements.map((statement) => ({ statement, status: "executed" })))
  );
}

/** 模型里改一列(数据库里的表变了)。 */
function withColumn(model: dbdriver.AlterTableModel, name: string, patch: Partial<dbdriver.ColumnDefinition>) {
  return new dbdriver.AlterTableModel({
    ...model,
    columns: model.columns.map((c) => (c.name === name ? { ...c, ...patch } : c)),
  });
}

function press(binding: ShortcutBinding, target: EventTarget = document.body): KeyboardEvent {
  const event = new KeyboardEvent("keydown", {
    code: binding.code,
    metaKey: isMac ? binding.mod : false,
    ctrlKey: isMac ? binding.ctrl : binding.mod,
    shiftKey: binding.shift,
    altKey: binding.alt,
    bubbles: true,
    cancelable: true,
  });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

beforeEach(() => {
  vi.mocked(GetTableMetadata).mockReset();
  vi.mocked(GetAlterTableModel).mockReset();
  vi.mocked(GetAlterTableStatements).mockReset();
  vi.mocked(ExecuteSchemaStatements).mockReset();
  vi.mocked(ListTables).mockReset();
  vi.mocked(ListTables).mockResolvedValue(["orders"]);
  vi.mocked(notifySuccess).mockReset();
  useShortcutStore.setState({ shortcuts: { ...DEFAULT_SHORTCUTS }, isRecording: false });
});

describe("保存:确认", () => {
  it("列出驱动给的全部语句并编号;删除了列时红色警告列出列与随之删除的索引,确认按钮为危险样式;取消不执行", async () => {
    await openMysql();
    renameColumn("status", "state");
    deleteColumn("note");
    const dialog = await clickSave();

    expect(within(dialog).getByText("query.designer.save.confirmTitle")).toBeInTheDocument();
    const statements = statementsIn(dialog);
    expect(statements.map((s) => s.textContent)).toEqual([
      expect.stringMatching(/^1MODIFY .*"name":"state"/),
      "2DROP note",
    ]);
    const warning = within(dialog).getByTestId("save-drop-warning");
    expect(within(warning).getByText("note")).toBeInTheDocument();
    expect(within(warning).getByText("idx_note")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "query.designer.save.run" })).toHaveAttribute(
      "data-variant",
      "destructive"
    );

    fireEvent.click(within(dialog).getByRole("button", { name: "action.cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(ExecuteSchemaStatements).not.toHaveBeenCalled();
    expect(nameInput("state")).toBeInTheDocument();
  });

  it("没有删除时没有警告,确认按钮是普通样式;说明按驱动是否整批可回滚", async () => {
    await openMysql();
    renameColumn("status", "state");
    const dialog = await clickSave();
    expect(within(dialog).queryByTestId("save-drop-warning")).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "query.designer.save.run" })).toHaveAttribute(
      "data-variant",
      "default"
    );
    expect(within(dialog).getByText("query.designer.save.confirmDesc")).toBeInTheDocument();
  });

  it("确认后执行的正是确认里列出的语句;执行期间不能再点,显示进度", async () => {
    await openMysql();
    renameColumn("status", "state");
    deleteColumn("note");
    const dialog = await clickSave();
    let finish: (r: query_svc.SchemaBatchResult) => void = () => {};
    vi.mocked(ExecuteSchemaStatements).mockReturnValue(new Promise((resolve) => (finish = resolve)));

    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.run" }));
    const statements = vi.mocked(GetAlterTableStatements).mock.results.at(-1)!.value as Promise<{
      statements: string[];
    }>;
    expect(ExecuteSchemaStatements).toHaveBeenCalledWith(1, "appdb", (await statements).statements);
    expect(within(dialog).getByRole("button", { name: /query\.designer\.save\.running/ })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "action.cancel" })).toBeDisabled();

    await act(async () => finish(batchResult([])));
  });
});

describe("保存:结果", () => {
  it("全部成功:顶部成功提示,重新读取表结构,刷新树的表列表;设计器与按新结构刚打开时一样", async () => {
    const holder = { model: mysqlModel() };
    await openMysql(holder);
    renameColumn("status", "state");
    await clickSave();
    succeedAll();
    vi.mocked(GetAlterTableModel).mockClear();
    holder.model = withColumn(holder.model, "status", { name: "state" });

    fireEvent.click(screen.getByRole("button", { name: "query.designer.save.run" }));

    await waitFor(() => expect(notifySuccess).toHaveBeenCalledWith("query.designer.save.done"));
    await waitFor(() => expect(GetAlterTableModel).toHaveBeenCalledWith(1, "appdb", "orders"));
    expect(ListTables).toHaveBeenCalledWith(1, "appdb");
    expect(await screen.findByDisplayValue("state")).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(screen.getByTestId("designer-changes")).toHaveTextContent("query.designer.changes.none");
    expect(db().dirtyInnerTabIds ?? []).not.toContain(INNER_ID);
  });

  it("改了表名:标签换成新名字,按新名字重新读取,树刷新", async () => {
    const holder = { model: mysqlModel() };
    await openMysql(holder);
    renameTable("orders_v2");
    await clickSave();
    succeedAll();
    vi.mocked(ListTables).mockResolvedValue(["orders_v2"]);
    vi.mocked(GetAlterTableModel).mockClear();
    holder.model = new dbdriver.AlterTableModel({ ...holder.model, name: "orders_v2" });

    fireEvent.click(screen.getByRole("button", { name: "query.designer.save.run" }));

    await waitFor(() =>
      expect(db().innerTabs[0]).toMatchObject({ id: "designer:appdb.orders_v2", table: "orders_v2" })
    );
    expect(db().activeInnerTabId).toBe("designer:appdb.orders_v2");
    await waitFor(() => expect(GetAlterTableModel).toHaveBeenCalledWith(1, "appdb", "orders_v2"));
    expect(await screen.findByDisplayValue("orders_v2")).toBeInTheDocument();
    expect(screen.getByTestId("designer-changes")).toHaveTextContent("query.designer.changes.none");
    await waitFor(() => expect(db().tables.appdb).toEqual(["orders_v2"]));
  });

  it("整批回滚:留在对话框里给出失败的语句与数据库错误,说明表结构没变;草稿原样保留,不重新读取", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_type"], transactionalDDL: true }), legacyModel());
    renameColumn("STATUS", "STATE");
    deleteColumn("ID");
    const dialog = await clickSave();
    expect(within(dialog).getByText("query.designer.save.confirmDescAtomic")).toBeInTheDocument();
    vi.mocked(ExecuteSchemaStatements).mockImplementation(async (_a, _d, statements) =>
      batchResult(
        [
          { statement: statements[0], status: "executed" },
          { statement: statements[1], status: "failed", error: "ORA-00942: table or view does not exist" },
        ],
        { atomic: true, rolledBack: true }
      )
    );
    vi.mocked(GetAlterTableModel).mockClear();

    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.run" }));

    expect(await within(dialog).findByText("query.designer.save.rolledBackTitle")).toBeInTheDocument();
    expect(within(dialog).getByText("query.designer.save.rolledBackDesc")).toBeInTheDocument();
    expect(within(dialog).getByText("ORA-00942: table or view does not exist")).toBeInTheDocument();
    expect(statementsIn(dialog).map((s) => s.dataset.status)).toEqual(["rolledBack", "failed"]);
    expect(GetAlterTableModel).not.toHaveBeenCalled();
    expect(ListTables).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.back" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(nameInput("STATE")).toBeInTheDocument();
    expect(deletedRow("ID")).toHaveAttribute("data-status", "deleted");
  });

  it("逐条执行停在中途:结果视图逐条标出;重新读取后已生效的改动不再标记,没执行的留在草稿里", async () => {
    const holder = { model: mysqlModel() };
    await openMysql(holder);
    renameColumn("status", "state");
    const added = addField();
    fireEvent.change(within(added).getByRole("textbox", { name: "query.designer.column.name" }), {
      target: { value: "paid_at" },
    });
    deleteColumn("note");
    const dialog = await clickSave();
    vi.mocked(ExecuteSchemaStatements).mockImplementation(async (_a, _d, statements) =>
      batchResult([
        { statement: statements[0], status: "executed" },
        { statement: statements[1], status: "failed", error: "Error 1060: Duplicate column name 'paid_at'" },
        { statement: statements[2], status: "notExecuted" },
      ])
    );
    vi.mocked(GetAlterTableModel).mockClear();
    holder.model = withColumn(holder.model, "status", { name: "state" });

    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.run" }));

    const result = await screen.findByRole("alertdialog");
    expect(await within(result).findByText("query.designer.save.partialTitle")).toBeInTheDocument();
    expect(statementsIn(result).map((s) => s.dataset.status)).toEqual(["executed", "failed", "notExecuted"]);
    expect(within(result).getByText("Error 1060: Duplicate column name 'paid_at'")).toBeInTheDocument();
    await waitFor(() => expect(GetAlterTableModel).toHaveBeenCalledTimes(1));
    expect(ListTables).toHaveBeenCalledWith(1, "appdb");

    fireEvent.click(within(result).getByRole("button", { name: "query.designer.save.back" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    // 改名已生效:不再标记;加列与删列没执行:仍在草稿里。
    expect(await screen.findByDisplayValue("state")).toBeInTheDocument();
    expect(rowOf("state")).toHaveAttribute("data-status", "unchanged");
    expect(rowOf("paid_at")).toHaveAttribute("data-status", "new");
    expect(deletedRow("note")).toHaveAttribute("data-status", "deleted");
    expect(db().dirtyInnerTabIds).toContain(INNER_ID);
  });

  it("逐条执行时改名已生效、后面失败:标签跟随新名字,结果视图仍在,没执行的改动留在草稿里", async () => {
    const holder = { model: mysqlModel() };
    await openMysql(holder);
    renameTable("orders_v2");
    deleteColumn("note");
    const dialog = await clickSave();
    vi.mocked(ExecuteSchemaStatements).mockImplementation(async (_a, _d, statements) =>
      batchResult([
        { statement: statements[0], status: "executed" },
        { statement: statements[1], status: "failed", error: "Error 1091: Can't DROP 'note'" },
      ])
    );
    vi.mocked(ListTables).mockResolvedValue(["orders_v2"]);
    holder.model = new dbdriver.AlterTableModel({ ...holder.model, name: "orders_v2" });

    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.run" }));

    await waitFor(() => expect(db().innerTabs[0]).toMatchObject({ id: "designer:appdb.orders_v2" }));
    const result = await screen.findByRole("alertdialog");
    expect(within(result).getByText("query.designer.save.partialTitle")).toBeInTheDocument();
    expect(within(result).getByText("Error 1091: Can't DROP 'note'")).toBeInTheDocument();
    fireEvent.click(within(result).getByRole("button", { name: "query.designer.save.back" }));
    expect(await screen.findByDisplayValue("orders_v2")).toBeInTheDocument();
    expect(deletedRow("note")).toHaveAttribute("data-status", "deleted");
    expect(screen.getByRole("textbox", { name: "query.designer.tableName" })).not.toHaveClass("bg-warning/10");
  });

  it("执行没能开始(绑定被拒):显示错误,什么都没执行,草稿保留,不重新读取", async () => {
    await openMysql();
    renameColumn("status", "state");
    const dialog = await clickSave();
    vi.mocked(ExecuteSchemaStatements).mockRejectedValue("dial tcp 10.0.0.1:3306: connection refused");
    vi.mocked(GetAlterTableModel).mockClear();

    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.run" }));

    expect(await within(dialog).findByText("dial tcp 10.0.0.1:3306: connection refused")).toBeInTheDocument();
    expect(within(dialog).getByText("query.designer.save.failedTitle")).toBeInTheDocument();
    expect(within(dialog).getByText("query.designer.save.failedDesc")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.back" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(nameInput("state")).toBeInTheDocument();
    expect(GetAlterTableModel).not.toHaveBeenCalled();
    expect(notifySuccess).not.toHaveBeenCalled();
  });
});

describe("保存快捷键", () => {
  beforeEach(() => {
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
  });

  it("设计表标签在前台且能保存时打开保存确认(焦点在输入框里也一样)", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    const event = press(DEFAULT_SHORTCUTS["designer.save"], nameInput("state"));
    expect(event.defaultPrevented).toBe(true);
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();
  });

  it("不能保存(没有改动)时不响应,也不拦下按键", async () => {
    await openMysql();
    await waitFor(() => expect(GetAlterTableStatements).toHaveBeenCalled());
    const event = press(DEFAULT_SHORTCUTS["designer.save"]);
    expect(event.defaultPrevented).toBe(false);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("设计器标签不在前台时不响应", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    act(() => useQueryStore.getState().openSqlTab(TAB_ID, "appdb"));
    const event = press(DEFAULT_SHORTCUTS["designer.save"]);
    expect(event.defaultPrevented).toBe(false);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("外层查询标签不在前台时不响应", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    act(() => useTabStore.setState({ activeTabId: "other" }));
    press(DEFAULT_SHORTCUTS["designer.save"]);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("按快捷键设置里改过的绑定响应", async () => {
    const rebound: ShortcutBinding = { code: "KeyS", mod: true, ctrl: false, shift: true, alt: false };
    useShortcutStore.getState().updateShortcut("designer.save", rebound);
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    press(DEFAULT_SHORTCUTS["designer.save"]);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    press(rebound);
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();
    useShortcutStore.getState().resetAll();
  });
});

describe("脏标记与关闭拦截", () => {
  it("有改动时上报给查询 store(外层标签随之标为未保存),撤销全部改动后清除", async () => {
    await openMysql();
    expect(db().dirtyInnerTabIds ?? []).not.toContain(INNER_ID);
    renameColumn("status", "state");
    await waitFor(() => expect(db().dirtyInnerTabIds).toContain(INNER_ID));
    expect(useTabStore.getState().unsavedTabIds).toContain(TAB_ID);

    fireEvent.click(screen.getByRole("button", { name: "query.designer.revertAll" }));
    fireEvent.click(await screen.findByRole("button", { name: "query.designer.revertAllConfirm" }));
    await waitFor(() => expect(db().dirtyInnerTabIds).not.toContain(INNER_ID));
    expect(useTabStore.getState().unsavedTabIds).not.toContain(TAB_ID);
  });

  it("关闭有改动的设计器标签:取消留下,放弃并关闭关掉", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(db().dirtyInnerTabIds).toContain(INNER_ID));

    act(() => useQueryStore.getState().closeInnerTab(TAB_ID, INNER_ID));
    let guard = await screen.findByRole("alertdialog");
    expect(within(guard).getByText("query.designer.guard.title")).toBeInTheDocument();
    fireEvent.click(within(guard).getByRole("button", { name: "action.cancel" }));
    await waitFor(() => expect(db().closeRequest).toBeUndefined());
    expect(db().innerTabs.map((t) => t.id)).toEqual([INNER_ID]);

    act(() => useQueryStore.getState().closeInnerTab(TAB_ID, INNER_ID));
    guard = await screen.findByRole("alertdialog");
    fireEvent.click(within(guard).getByRole("button", { name: "query.unsavedEditsDiscardClose" }));
    await waitFor(() => expect(db().innerTabs).toEqual([]));
    expect(useTabStore.getState().unsavedTabIds).not.toContain(TAB_ID);
  });

  it("关闭拦截里「保存…」打开保存确认,全部成功后关闭标签", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    act(() => useQueryStore.getState().closeInnerTab(TAB_ID, INNER_ID));
    const guard = await screen.findByRole("alertdialog");
    fireEvent.click(within(guard).getByRole("button", { name: "query.designer.guard.save" }));

    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("query.designer.save.confirmTitle")).toBeInTheDocument();
    succeedAll();
    fireEvent.click(within(confirm).getByRole("button", { name: "query.designer.save.run" }));

    await waitFor(() => expect(db().innerTabs).toEqual([]));
    expect(notifySuccess).toHaveBeenCalled();
  });

  it("「保存…」后执行停在中途:标签不关,显示结果", async () => {
    await openMysql();
    renameColumn("status", "state");
    deleteColumn("note");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    act(() => useQueryStore.getState().closeInnerTab(TAB_ID, INNER_ID));
    fireEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "query.designer.guard.save" })
    );
    vi.mocked(ExecuteSchemaStatements).mockImplementation(async (_a, _d, statements) =>
      batchResult([
        { statement: statements[0], status: "executed" },
        { statement: statements[1], status: "failed", error: "boom" },
      ])
    );
    fireEvent.click(await screen.findByRole("button", { name: "query.designer.save.run" }));

    expect(await screen.findByText("query.designer.save.partialTitle")).toBeInTheDocument();
    expect(db().innerTabs.map((t) => t.id)).toEqual([INNER_ID]);
    expect(db().closeRequest).toBeUndefined();
  });

  it("设计器不在前台时关闭它(关闭其他标签)同样拦截", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(db().dirtyInnerTabIds).toContain(INNER_ID));
    act(() => useQueryStore.getState().openSqlTab(TAB_ID, "appdb"));
    const sqlId = db().activeInnerTabId!;

    act(() => useQueryStore.getState().closeOtherInnerTabs(TAB_ID, sqlId));
    const guard = await screen.findByRole("alertdialog");
    expect(within(guard).getByText("query.designer.guard.title")).toBeInTheDocument();
    fireEvent.click(within(guard).getByRole("button", { name: "query.unsavedEditsDiscardClose" }));
    await waitFor(() => expect(db().innerTabs.map((t) => t.id)).toEqual([sqlId]));
  });

  it("关闭整个查询标签:「保存…」全部成功后连外层标签一起关", async () => {
    await openMysql();
    renameColumn("status", "state");
    await waitFor(() => expect(saveButton()).toBeEnabled());
    act(() => useQueryStore.getState().requestCloseInnerTabs(TAB_ID, [INNER_ID], { closeOuter: true }));
    fireEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "query.designer.guard.save" })
    );
    succeedAll();
    fireEvent.click(await screen.findByRole("button", { name: "query.designer.save.run" }));

    await waitFor(() => expect(useTabStore.getState().tabs.map((t) => t.id)).not.toContain(TAB_ID));
  });
});
