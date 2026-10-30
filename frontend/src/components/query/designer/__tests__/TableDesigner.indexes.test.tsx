import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { dbdriver } from "../../../../../wailsjs/go/models";
import {
  DatabaseDriverOwner,
  GetAlterTableModel,
  GetAlterTableStatements,
  GetTableMetadata,
} from "../../../../../wailsjs/go/query/Query";
import { useTabStore } from "@/stores/tabStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { BUILTIN_DRIVERS, fakeAlterDriver } from "@/__tests__/dbDriverFixtures";
import {
  lastRequest,
  legacyModel,
  mysqlModel,
  openMysql,
  openSqlTab,
  openWith,
  rowOf,
  saveButton,
} from "./designerKit";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

beforeEach(() => {
  vi.mocked(GetTableMetadata).mockReset();
  vi.mocked(GetAlterTableModel).mockReset();
  vi.mocked(GetAlterTableStatements).mockReset();
  vi.mocked(DatabaseDriverOwner).mockReset();
});

const MSSQL = BUILTIN_DRIVERS.find((d) => d.id === "mssql")!;

function openIndexes() {
  fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.indexes/ }));
}

function indexList() {
  return screen.getByTestId("designer-index-list");
}

function indexRow(name: string): HTMLElement {
  return within(indexList()).getByText(name).closest('[role="row"]') as HTMLElement;
}

function editor() {
  return screen.getByTestId("designer-index-editor");
}

/** 在编辑区里给索引加一列(从下拉里点选)。 */
async function pickColumn(name: string) {
  fireEvent.click(within(editor()).getByRole("button", { name: "query.designer.addIndexColumn" }));
  const list = await screen.findByTestId("designer-index-column-list");
  fireEvent.click(within(list).getByRole("option", { name: new RegExp(`^${name}`) }));
}

/** 带只读、不可删除索引与主键的 MySQL 表。 */
function richModel(): dbdriver.AlterTableModel {
  const model = mysqlModel();
  model.indexes = [
    ...model.indexes!,
    new dbdriver.IndexDefinition({
      name: "idx_lower",
      type: "normal",
      columns: [{ name: "lower(note)", expression: true }],
      readOnlyReason: "表达式索引,只能删除",
    }),
    new dbdriver.IndexDefinition({ name: "uk_auto", type: "unique", columns: [{ name: "status" }], undroppable: true }),
  ];
  return model;
}

describe("设计表:索引编辑", () => {
  it("新建索引:编辑区打开,名字按 idx_表_列 生成并跟随列;请求里是新索引,列表与计数标出新增", async () => {
    await openMysql();
    openIndexes();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.addIndex" }));
    const name = within(editor()).getByRole("textbox", { name: "query.designer.indexName" });
    expect(name).toHaveValue("idx_orders");
    await pickColumn("status");
    expect(name).toHaveValue("idx_orders_status");
    expect(indexRow("idx_orders_status")).toHaveAttribute("data-status", "new");
    expect(screen.getByTestId("designer-changes")).toHaveTextContent("query.designer.tab.indexes+1");

    await waitFor(() =>
      expect(lastRequest().indexes.at(-1)).toEqual({
        originalName: "",
        name: "idx_orders_status",
        type: "normal",
        columns: [{ name: "status" }],
      })
    );
    // 手动改过名字后不再跟随列。
    fireEvent.change(name, { target: { value: "by_status" } });
    await pickColumn("note");
    expect(name).toHaveValue("by_status");
  });

  it("可选的列包括尚未保存的新列,已在索引里的列不能再选", async () => {
    await openMysql();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.addField" }));
    const fields = screen.getAllByRole("textbox", { name: "query.designer.column.name" });
    fireEvent.change(fields[fields.length - 1], { target: { value: "paid_at" } });
    openIndexes();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.addIndex" }));
    await pickColumn("paid_at");
    fireEvent.click(within(editor()).getByRole("button", { name: "query.designer.addIndexColumn" }));
    const list = await screen.findByTestId("designer-index-column-list");
    expect(within(list).getByRole("option", { name: /^paid_at/ })).toBeDisabled();
    expect(within(list).getByRole("option", { name: /^paid_at/ })).toHaveTextContent("query.designer.newColumn");
  });

  it("编辑区:按声明给出降序、前缀、方法与注释;列可上下移与移除;改已有索引提示重建", async () => {
    await openMysql();
    openIndexes();
    fireEvent.click(within(indexRow("idx_note")).getByText("idx_note"));
    const panel = editor();
    expect(within(panel).getByText("query.designer.indexRebuildNote")).toBeInTheDocument();
    await pickColumn("status");
    const columns = within(panel).getAllByTestId("index-editor-column");
    expect(columns.map((c) => within(c).getByTestId("index-editor-column-name").textContent)).toEqual([
      "note",
      "status",
    ]);
    fireEvent.click(within(columns[1]).getByRole("button", { name: "query.designer.moveUp" }));
    fireEvent.click(
      within(within(panel).getAllByTestId("index-editor-column")[1]).getByRole("button", {
        name: "query.designer.descending",
      })
    );
    fireEvent.change(within(within(panel).getAllByTestId("index-editor-column")[1]).getByRole("spinbutton"), {
      target: { value: "8" },
    });
    fireEvent.change(within(panel).getByRole("textbox", { name: "query.designer.column.comment" }), {
      target: { value: "按备注" },
    });
    expect(within(panel).getByRole("combobox", { name: "query.designer.indexOption.method" })).toBeInTheDocument();
    expect(indexRow("idx_note")).toHaveAttribute("data-status", "modified");

    await waitFor(() =>
      expect(lastRequest().indexes.find((i: { originalName: string }) => i.originalName === "idx_note")).toEqual({
        originalName: "idx_note",
        name: "idx_note",
        type: "normal",
        columns: [{ name: "status" }, { name: "note", descending: true, prefixLength: 8 }],
        comment: "按备注",
      })
    );
    fireEvent.click(
      within(within(panel).getAllByTestId("index-editor-column")[0]).getByRole("button", {
        name: "query.designer.removeIndexColumn",
      })
    );
    expect(within(panel).getAllByTestId("index-editor-column")).toHaveLength(1);
  });

  it("删除已有索引只做标记并可恢复,删除新索引直接移除", async () => {
    await openMysql();
    openIndexes();
    fireEvent.click(within(indexRow("idx_note")).getByRole("button", { name: "query.designer.deleteIndex" }));
    expect(indexRow("idx_note")).toHaveAttribute("data-status", "deleted");
    await waitFor(() => expect(lastRequest().indexDrops).toEqual(["idx_note"]));
    fireEvent.click(within(indexRow("idx_note")).getByRole("button", { name: "query.designer.restoreIndex" }));
    expect(indexRow("idx_note")).toHaveAttribute("data-status", "unchanged");

    fireEvent.click(screen.getByRole("button", { name: "query.designer.addIndex" }));
    fireEvent.click(within(indexRow("idx_orders")).getByRole("button", { name: "query.designer.deleteIndex" }));
    expect(within(indexList()).queryByText("idx_orders")).not.toBeInTheDocument();
  });

  it("只删除已有索引(不删列):SQL 预览顶部红色警告列出它,保存确认同样警告、确认按钮为危险样式", async () => {
    await openMysql();
    openIndexes();
    fireEvent.click(within(indexRow("idx_note")).getByRole("button", { name: "query.designer.deleteIndex" }));
    openSqlTab();
    const warning = await screen.findByTestId("designer-drop-warning");
    expect(within(warning).getByText("idx_note")).toBeInTheDocument();
    expect(within(warning).getByText("query.designer.sql.dropIndexesOnly")).toBeInTheDocument();

    await waitFor(() => expect(saveButton()).toBeEnabled());
    fireEvent.click(saveButton());
    const dialog = await screen.findByRole("alertdialog");
    expect(within(within(dialog).getByTestId("save-drop-warning")).getByText("idx_note")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "query.designer.save.run" })).toHaveAttribute(
      "data-variant",
      "destructive"
    );
  });

  it("删除列:引用它的索引标记删除并注明随列删除,恢复列一起恢复;删除警告列出它", async () => {
    await openMysql();
    fireEvent.click(within(rowOf("note")).getByRole("button", { name: "query.designer.deleteColumn" }));
    openIndexes();
    expect(indexRow("idx_note")).toHaveAttribute("data-status", "deleted");
    expect(within(indexRow("idx_note")).getByText("query.designer.indexDroppedWith")).toBeInTheDocument();
    expect(within(indexRow("idx_note")).queryByRole("button", { name: "query.designer.restoreIndex" })).toBeNull();
    openSqlTab();
    const warning = await screen.findByTestId("designer-drop-warning");
    expect(within(warning).getByText("idx_note")).toBeInTheDocument();
    // 随列删除的索引留给驱动:请求里既不保留也不列在 indexDrops。
    await waitFor(() =>
      expect(lastRequest().indexes.map((i: { originalName: string }) => i.originalName)).toEqual(["PRIMARY"])
    );
    expect(lastRequest().indexDrops).toEqual([]);

    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.fields/ }));
    fireEvent.click(await screen.findByRole("button", { name: "query.designer.restoreColumn" }));
    openIndexes();
    expect(indexRow("idx_note")).toHaveAttribute("data-status", "unchanged");
  });

  it("只读索引带锁与原因,不能编辑、可以删除;不可删除的索引没有删除按钮", async () => {
    await openMysql({ model: richModel() });
    openIndexes();
    const readOnly = indexRow("idx_lower");
    expect(within(readOnly).getByLabelText("query.designer.indexReadOnly")).toBeInTheDocument();
    expect(within(readOnly).getByRole("button", { name: "query.designer.deleteIndex" })).toBeInTheDocument();
    fireEvent.click(within(readOnly).getByText("idx_lower"));
    expect(within(editor()).getByText("表达式索引,只能删除")).toBeInTheDocument();
    expect(within(editor()).queryByRole("textbox", { name: "query.designer.indexName" })).not.toBeInTheDocument();

    expect(within(indexRow("uk_auto")).queryByRole("button", { name: "query.designer.deleteIndex" })).toBeNull();
  });

  it("驱动不能改主键:已有主键锁定、不能删,类型里「主键」不可选", async () => {
    const descriptor = new dbdriver.Descriptor({
      ...BUILTIN_DRIVERS.find((d) => d.id === "mysql")!,
      id: "nopk",
      indexEditing: { ...BUILTIN_DRIVERS.find((d) => d.id === "mysql")!.indexEditing!, alterPrimaryKey: false },
    });
    await openWith(descriptor, mysqlModel());
    openIndexes();
    expect(within(indexRow("PRIMARY")).queryByRole("button", { name: "query.designer.deleteIndex" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.addIndex" }));
    const types = within(editor()).getByRole("radiogroup", { name: "query.designer.column.type" });
    expect(within(types).getByRole("radio", { name: "query.designer.indexType.primary" })).toBeDisabled();
  });

  it("校验:没有列的索引是错误,保存不可用,点问题跳到索引的编辑区", async () => {
    await openMysql();
    openIndexes();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.addIndex" }));
    fireEvent.click(within(editor()).getByRole("button", { name: "query.designer.closeIndexEditor" }));
    const issues = screen.getByTestId("designer-issues");
    expect(issues).toHaveTextContent("query.designer.issue.indexColumnsEmpty");
    expect(saveButton()).toBeDisabled();
    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.fields/ }));
    fireEvent.click(within(issues).getByRole("button", { name: /indexColumnsEmpty/ }));
    await waitFor(() => expect(editor()).toBeInTheDocument());
    expect(screen.getByRole("tab", { name: /query\.designer\.tab\.indexes/ })).toHaveAttribute("data-state", "active");
  });

  it("声明为独占的开关(SQL Server 聚集)打开在两个索引上是错误", async () => {
    const model = mysqlModel();
    model.indexes = [
      new dbdriver.IndexDefinition({
        name: "PK_orders",
        type: "primary",
        columns: [{ name: "id" }],
        options: { clustered: true },
      }),
    ];
    await openWith(MSSQL, model);
    openIndexes();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.addIndex" }));
    await pickColumn("status");
    fireEvent.click(within(editor()).getByRole("switch", { name: "query.designer.indexOption.clustered" }));
    expect(screen.getByTestId("designer-issues")).toHaveTextContent("query.designer.issue.indexOptionExclusive");
  });
});

describe("驱动没声明索引编辑", () => {
  const legacyDriver = fakeAlterDriver({ alterTableAttributes: [] });

  it("扩展驱动:索引页只读,说明后有「去更新」,打开设置里的扩展页", async () => {
    vi.mocked(DatabaseDriverOwner).mockResolvedValue("oracle");
    await openWith(legacyDriver, legacyModel());
    openIndexes();
    expect(screen.getByText("query.designer.indexesNotEditable")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "query.designer.addIndex" })).not.toBeInTheDocument();
    const update = await screen.findByRole("button", { name: "query.designer.updateDriver" });
    expect(DatabaseDriverOwner).toHaveBeenCalledWith(legacyDriver.id);
    act(() => fireEvent.click(update));
    expect(useTabStore.getState().activeTabId).toBe("settings");
    expect(useSettingsUiStore.getState().activeTab).toBe("extensions");
  });

  it("内置驱动(不归任何扩展):没有「去更新」;请求里不带索引字段", async () => {
    vi.mocked(DatabaseDriverOwner).mockResolvedValue("");
    await openWith(legacyDriver, legacyModel());
    openIndexes();
    expect(screen.getByText("query.designer.indexesNotEditable")).toBeInTheDocument();
    await waitFor(() => expect(DatabaseDriverOwner).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "query.designer.updateDriver" })).not.toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.fields/ }));
    fireEvent.change(screen.getByDisplayValue("STATUS"), { target: { value: "STATE" } });
    await waitFor(() => expect(GetAlterTableStatements).toHaveBeenCalled());
    expect(lastRequest()).not.toHaveProperty("indexes");
    expect(lastRequest()).not.toHaveProperty("indexDrops");
  });
});
