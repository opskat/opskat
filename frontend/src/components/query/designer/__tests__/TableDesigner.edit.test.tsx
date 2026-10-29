import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { useQueryStore } from "@/stores/queryStore";
import { toast } from "sonner";
import { notifyCopied } from "@/lib/notify";
import { query_svc } from "../../../../../wailsjs/go/models";
import { GetAlterTableModel, GetAlterTableStatements, GetTableMetadata } from "../../../../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, fakeAlterDriver, provideDrivers } from "@/__tests__/dbDriverFixtures";
import {
  addField,
  lastRow,
  lastRequest,
  legacyModel,
  openMysql,
  openSqlTab,
  openWith,
  renderDesigner,
  rowOf,
  saveButton,
  setup,
} from "./designerKit";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

beforeEach(() => {
  vi.mocked(GetTableMetadata).mockReset();
  vi.mocked(GetAlterTableModel).mockReset();
  vi.mocked(GetAlterTableStatements).mockReset();
  vi.mocked(notifyCopied).mockReset();
});

describe("设计表:字段编辑", () => {
  it("工具栏是表名与表注释输入,状态栏是驱动名与表的全名", async () => {
    await openMysql();
    expect(screen.getByRole("textbox", { name: "query.designer.tableName" })).toHaveValue("orders");
    expect(screen.getByRole("textbox", { name: "query.designer.tableComment" })).toHaveValue("销售订单");
    expect(screen.queryByTestId("designer-readonly")).not.toBeInTheDocument();
    expect(screen.getByTestId("designer-status")).toHaveTextContent("MySQL");
    expect(screen.getByTestId("designer-status")).toHaveTextContent("appdb.orders");
    expect(screen.getByText("query.designer.changes.none")).toBeInTheDocument();
  });

  it("驱动没声明表注释时不显示表注释输入", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_type"] }));
    expect(screen.queryByRole("textbox", { name: "query.designer.tableComment" })).not.toBeInTheDocument();
  });

  it("改过的格子标黄并带原值,行标为修改;计数随之变化;列属性里可逐项还原", async () => {
    await openMysql();
    const row = rowOf("status");
    fireEvent.change(within(row).getByRole("textbox", { name: "query.designer.column.type" }), {
      target: { value: "varchar(32)" },
    });
    expect(row).toHaveAttribute("data-status", "modified");
    const cell = within(row).getByDisplayValue("varchar(32)").closest("[data-changed]") as HTMLElement;
    expect(cell).toHaveAttribute("data-changed", "true");
    expect(cell).toHaveAttribute("title", "query.designer.originalValue");
    expect(screen.getByTestId("designer-changes")).toHaveTextContent("~1");

    fireEvent.click(row);
    const props = screen.getByTestId("designer-column-props");
    expect(within(props).getByText("varchar(16)")).toBeInTheDocument();
    fireEvent.click(within(props).getByRole("button", { name: "query.designer.revert" }));
    expect(within(rowOf("status")).getByDisplayValue("varchar(16)")).toBeInTheDocument();
    expect(rowOf("status")).toHaveAttribute("data-status", "unchanged");
    expect(screen.getByText("query.designer.changes.none")).toBeInTheDocument();
  });

  it("删除已有列只做标记,可恢复;删除新增的行直接移除", async () => {
    await openMysql();
    fireEvent.click(within(rowOf("note")).getByRole("button", { name: "query.designer.deleteColumn" }));
    const deleted = screen.getByText("note").closest('[role="row"]') as HTMLElement;
    expect(deleted).toHaveAttribute("data-status", "deleted");
    expect(screen.getByTestId("designer-changes")).toHaveTextContent("−1");
    fireEvent.click(within(deleted).getByRole("button", { name: "query.designer.restoreColumn" }));
    expect(rowOf("note")).toHaveAttribute("data-status", "unchanged");

    const added = addField();
    expect(added).toHaveAttribute("data-status", "new");
    const rows = screen.getAllByRole("row").length;
    fireEvent.click(within(added).getByRole("button", { name: "query.designer.deleteColumn" }));
    expect(screen.getAllByRole("row")).toHaveLength(rows - 1);
  });

  it("驱动能力有限:表格上方说明由声明生成,已有列锁定没声明的属性,新列不锁", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_type", "column_nullable"] }));
    expect(screen.getByTestId("designer-capability")).toHaveTextContent("query.designer.capability.limited");
    const row = rowOf("STATUS");
    expect(within(row).getByLabelText("query.designer.lockReason")).toBeInTheDocument();
    expect(within(row).getByRole("textbox", { name: "query.designer.column.type" })).toBeInTheDocument();

    const added = addField();
    expect(within(added).queryByLabelText("query.designer.lockReason")).not.toBeInTheDocument();
    fireEvent.click(within(added).getByRole("textbox", { name: "query.designer.column.name" }));
    expect(
      within(screen.getByTestId("designer-column-props")).getByRole("textbox", { name: "query.designer.defaultValue" })
    ).toBeEnabled();
  });

  it("驱动声明了全部可改属性时没有能力说明", async () => {
    await openMysql();
    expect(screen.queryByTestId("designer-capability")).not.toBeInTheDocument();
  });

  it("类型下拉列出驱动声明的分组,点选填入;没声明类型时只有输入框", async () => {
    await openMysql();
    const row = rowOf("status");
    fireEvent.click(within(row).getByRole("button", { name: "query.designer.typeSuggestions" }));
    const list = await screen.findByTestId("designer-type-list");
    expect(within(list).getByText("query.designer.typeGroup.string")).toBeInTheDocument();
    fireEvent.click(within(list).getByRole("option", { name: "MEDIUMTEXT" }));
    expect(within(rowOf("status")).getByRole("textbox", { name: "query.designer.column.type" })).toHaveValue(
      "MEDIUMTEXT"
    );
  });

  it("没声明常用类型:没有下拉,新列类型留空且是错误", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_type"] }));
    expect(screen.queryByRole("button", { name: "query.designer.typeSuggestions" })).not.toBeInTheDocument();
    const added = addField();
    expect(within(added).getByRole("textbox", { name: "query.designer.column.type" })).toHaveValue("");
    expect(screen.getByTestId("designer-issues")).toHaveTextContent("query.designer.issue.columnTypeEmpty");
  });

  it("新列预填驱动声明的类型", async () => {
    await openMysql();
    const added = addField();
    expect(within(added).getByRole("textbox", { name: "query.designer.column.type" })).toHaveValue("VARCHAR(255)");
  });

  it("声明了默认值写法:列属性是 无 / NULL / 值 / 表达式;没声明时是单一文本输入", async () => {
    await openMysql();
    fireEvent.click(rowOf("status"));
    const props = screen.getByTestId("designer-column-props");
    const kinds = within(props).getByRole("radiogroup", { name: "query.designer.column.default" });
    expect(
      within(kinds)
        .getAllByRole("radio")
        .map((r) => r.textContent)
    ).toEqual([
      "query.designer.defaultKind.none",
      "NULL",
      "query.designer.defaultKind.value",
      "query.designer.defaultKind.expression",
    ]);
    expect(within(kinds).getByRole("radio", { name: "query.designer.defaultKind.value" })).toHaveAttribute(
      "aria-checked",
      "true"
    );
    fireEvent.click(within(kinds).getByRole("radio", { name: "query.designer.defaultKind.expression" }));
    fireEvent.change(within(props).getByRole("textbox", { name: "query.designer.defaultValue" }), {
      target: { value: "uuid()" },
    });
    expect(rowOf("status")).toHaveAttribute("data-status", "modified");
  });

  it("没声明默认值写法:单一文本输入,说明旧的写法规则", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_default"] }));
    fireEvent.click(rowOf("STATUS"));
    const props = screen.getByTestId("designer-column-props");
    expect(within(props).queryByRole("radiogroup")).not.toBeInTheDocument();
    expect(within(props).getByRole("textbox", { name: "query.designer.defaultValue" })).toHaveValue("'pending'");
    expect(within(props).getByText("query.designer.defaultHint.legacy")).toBeInTheDocument();
  });

  it("自增只在新列上可勾;已有列显示现状;没声明自增时不出现", async () => {
    await openMysql();
    fireEvent.click(rowOf("id"));
    const existing = within(screen.getByTestId("designer-column-props")).getByRole("checkbox", {
      name: "query.designer.autoIncrement",
    });
    expect(existing).toBeDisabled();
    expect(existing).toHaveAttribute("data-state", "checked");

    const added = addField();
    fireEvent.click(within(added).getByRole("textbox", { name: "query.designer.column.name" }));
    const fresh = within(screen.getByTestId("designer-column-props")).getByRole("checkbox", {
      name: "query.designer.autoIncrement",
    });
    expect(fresh).toBeEnabled();
  });

  it("自增只能在新表上设的驱动:已有表的新列不能勾自增,并说明原因", async () => {
    await openWith(fakeAlterDriver({ autoIncrement: true, autoIncrementNewTablesOnly: true }));
    addField();
    fireEvent.click(within(lastRow()).getByRole("textbox", { name: "query.designer.column.name" }));
    const props = screen.getByTestId("designer-column-props");
    expect(within(props).getByRole("checkbox", { name: "query.designer.autoIncrement" })).toBeDisabled();
    expect(within(props).getByText("query.designer.autoIncrementNewTablesOnly")).toBeInTheDocument();
  });

  it("没声明自增的驱动:列属性里没有自增", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_default"] }));
    addField();
    fireEvent.click(within(lastRow()).getByRole("textbox", { name: "query.designer.column.name" }));
    expect(
      within(screen.getByTestId("designer-column-props")).queryByRole("checkbox", {
        name: "query.designer.autoIncrement",
      })
    ).not.toBeInTheDocument();
  });

  it("名称折叠:新列名与规则不符时黄色提醒,不阻止保存", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: [], nameFolding: "upper" }), legacyModel());
    const added = addField();
    fireEvent.change(within(added).getByRole("textbox", { name: "query.designer.column.type" }), {
      target: { value: "DATE" },
    });
    fireEvent.change(within(added).getByRole("textbox", { name: "query.designer.column.name" }), {
      target: { value: "updated_at" },
    });
    const issues = screen.getByTestId("designer-issues");
    expect(issues).toHaveTextContent("query.designer.issue.nameFolding.upper");
    expect(issues).toHaveTextContent("query.designer.issues.warnings");
    expect(issues).not.toHaveTextContent("query.designer.issues.errors");
    expect(within(lastRow()).getByLabelText("query.designer.issue.nameFolding.upper")).toBeInTheDocument();
    await waitFor(() => expect(saveButton()).toBeEnabled());
  });

  it("校验:列名重名与为空标红并列在底部,点击问题跳到对应格子", async () => {
    await openMysql();
    const added = addField();
    const name = within(added).getByRole("textbox", { name: "query.designer.column.name" });
    fireEvent.change(name, { target: { value: "Status" } });
    expect(name.closest("[data-error]")).toHaveAttribute("data-error", "true");
    const issues = screen.getByTestId("designer-issues");
    expect(issues).toHaveTextContent("query.designer.issue.columnNameDuplicate");
    expect(screen.getByTestId("designer-issue-count")).toBeInTheDocument();
    expect(saveButton()).toBeDisabled();

    fireEvent.change(screen.getByRole("textbox", { name: "query.designer.tableName" }), { target: { value: "" } });
    fireEvent.click(within(issues).getByRole("button", { name: /query\.designer\.issue\.columnNameDuplicate/ }));
    await waitFor(() => expect(name).toHaveFocus());
    fireEvent.click(within(issues).getByRole("button", { name: /query\.designer\.issue\.tableNameEmpty/ }));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "query.designer.tableName" })).toHaveFocus());
  });

  it("从 SQL 预览点问题也会切回字段页并聚焦", async () => {
    await openMysql();
    const added = addField();
    fireEvent.change(within(added).getByRole("textbox", { name: "query.designer.column.name" }), {
      target: { value: "" },
    });
    openSqlTab();
    fireEvent.click(
      within(screen.getByTestId("designer-issues")).getByRole("button", {
        name: /query\.designer\.issue\.columnNameEmpty/,
      })
    );
    await waitFor(() =>
      expect(within(lastRow()).getByRole("textbox", { name: "query.designer.column.name" })).toHaveFocus()
    );
  });

  it("撤销全部改动与重新读取在有改动时先确认", async () => {
    await openMysql();
    fireEvent.change(within(rowOf("status")).getByRole("textbox", { name: "query.designer.column.name" }), {
      target: { value: "state" },
    });
    fireEvent.click(screen.getByRole("button", { name: "query.designer.revertAll" }));
    fireEvent.click(await screen.findByRole("button", { name: "query.designer.revertAllConfirm" }));
    expect(await screen.findByDisplayValue("status")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("state")).not.toBeInTheDocument();

    fireEvent.change(within(rowOf("status")).getByRole("textbox", { name: "query.designer.column.name" }), {
      target: { value: "state" },
    });
    vi.mocked(GetAlterTableModel).mockClear();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.reload" }));
    fireEvent.click(await screen.findByRole("button", { name: "query.designer.reloadConfirm" }));
    await waitFor(() => expect(GetAlterTableModel).toHaveBeenCalledTimes(1));
    expect(await screen.findByDisplayValue("status")).toBeInTheDocument();
  });

  it("没有改动时重新读取不确认", async () => {
    await openMysql();
    vi.mocked(GetAlterTableModel).mockClear();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.reload" }));
    await waitFor(() => expect(GetAlterTableModel).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("button", { name: "query.designer.reloadConfirm" })).not.toBeInTheDocument();
  });

  it("只读连接的资产以设计模式打开时仍是只读", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    setup("mysql", { readOnly: true });
    vi.mocked(GetTableMetadata).mockResolvedValue(
      new query_svc.TableMetadata({
        columns: [{ name: "status", type: "varchar(16)", nullable: true, primaryKey: false, comment: "" }],
        indexes: [],
        foreignKeys: [],
      })
    );
    await renderDesigner();
    expect(await screen.findByText("status")).toBeInTheDocument();
    expect(screen.getByTestId("designer-readonly")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /action\.save/ })).not.toBeInTheDocument();
  });
});

describe("设计表:SQL 预览", () => {
  it("原样交回读出的表:驱动不给语句,预览说没有改动,保存不可用", async () => {
    const model = await openMysql();
    await waitFor(() => expect(GetAlterTableStatements).toHaveBeenCalled());
    const req = lastRequest();
    expect(req.columns).toEqual(
      model.columns.map((c) => ({
        originalName: c.name,
        name: c.name,
        type: c.type,
        nullable: c.nullable,
        defaultValue: c.defaultValue,
        defaultKind: c.defaultKind,
        comment: c.comment,
      }))
    );
    expect(req.drops).toEqual([]);
    // 驱动声明了索引编辑:读出的索引原样交回(带原名),没有删除。
    expect(req.indexes).toEqual(model.indexes!.map((index) => ({ originalName: index.name, ...index })));
    expect(req.indexDrops).toEqual([]);
    openSqlTab();
    expect(await screen.findByText("query.designer.sql.noChanges")).toBeInTheDocument();
    expect(saveButton()).toBeDisabled();
  });

  it("草稿变化后按声明重新生成:声明了写法与自增的驱动,新列带写法与自增,按次序编号显示", async () => {
    await openMysql();
    const added = addField();
    fireEvent.change(within(added).getByRole("textbox", { name: "query.designer.column.name" }), {
      target: { value: "seq" },
    });
    fireEvent.click(
      within(screen.getByTestId("designer-column-props")).getByRole("checkbox", {
        name: "query.designer.autoIncrement",
      })
    );
    openSqlTab();
    const list = await screen.findByTestId("designer-statements");
    await waitFor(() => expect(within(list).getByText(/ADD /)).toBeInTheDocument());
    expect(lastRequest().columns[3]).toMatchObject({
      originalName: "",
      name: "seq",
      defaultKind: "none",
      autoIncrement: true,
    });
    expect(
      within(list)
        .getAllByTestId("designer-statement-no")
        .map((n) => n.textContent)
    ).toEqual(["1"]);
    await waitFor(() => expect(saveButton()).toBeEnabled());
    fireEvent.click(saveButton());
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/ADD /)).toBeInTheDocument();
  });

  it("没声明写法与自增的驱动:请求里不带这两个字段", async () => {
    await openWith(fakeAlterDriver({ alterTableAttributes: ["column_default"] }));
    fireEvent.click(rowOf("STATUS"));
    fireEvent.change(
      within(screen.getByTestId("designer-column-props")).getByRole("textbox", { name: "query.designer.defaultValue" }),
      { target: { value: "SYSDATE" } }
    );
    await waitFor(() => expect(lastRequest().columns[1].defaultValue).toBe("SYSDATE"));
    for (const column of lastRequest().columns) {
      expect(column).not.toHaveProperty("defaultKind");
      expect(column).not.toHaveProperty("autoIncrement");
    }
  });

  it("驱动报错时显示错误,保存不可用", async () => {
    await openMysql();
    vi.mocked(GetAlterTableStatements).mockRejectedValue("unsupported: column type change");
    fireEvent.change(within(rowOf("status")).getByRole("textbox", { name: "query.designer.column.type" }), {
      target: { value: "json" },
    });
    openSqlTab();
    expect(await screen.findByText("unsupported: column type change")).toBeInTheDocument();
    expect(saveButton()).toBeDisabled();
  });

  it("有错误时不请求驱动,预览说明先解决问题", async () => {
    await openMysql();
    await waitFor(() => expect(GetAlterTableStatements).toHaveBeenCalled());
    vi.mocked(GetAlterTableStatements).mockClear();
    fireEvent.change(screen.getByRole("textbox", { name: "query.designer.tableName" }), { target: { value: "" } });
    openSqlTab();
    expect(await screen.findByText("query.designer.sql.blocked")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 400));
    expect(GetAlterTableStatements).not.toHaveBeenCalled();
  });

  it("删除已有列:顶部红色警告列出列与引用它的索引", async () => {
    await openMysql();
    fireEvent.click(within(rowOf("note")).getByRole("button", { name: "query.designer.deleteColumn" }));
    openSqlTab();
    const warning = await screen.findByTestId("designer-drop-warning");
    expect(within(warning).getByText("note")).toBeInTheDocument();
    expect(within(warning).getByText("idx_note")).toBeInTheDocument();
  });

  it("复制全部语句;在 SQL 标签打开", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    await openMysql();
    fireEvent.click(within(rowOf("note")).getByRole("button", { name: "query.designer.deleteColumn" }));
    openSqlTab();
    await screen.findByText("DROP note");

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "action.copy" }));
    });
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("DROP note;");
    expect(notifyCopied).toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "query.designer.sql.openInSqlTab" }));
    const state = useQueryStore.getState().dbStates["query-1"];
    const sqlTab = state.innerTabs.find((t) => t.type === "sql");
    expect(sqlTab).toMatchObject({ type: "sql", sql: "DROP note;", selectedDb: "appdb" });
    expect(state.activeInnerTabId).toBe(sqlTab!.id);
  });

  it("复制失败(剪贴板拒绝):给出错误提示,不报复制成功", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error("clipboard denied")) },
    });
    const toastError = vi.spyOn(toast, "error");
    await openMysql();
    fireEvent.click(within(rowOf("note")).getByRole("button", { name: "query.designer.deleteColumn" }));
    openSqlTab();
    await screen.findByText("DROP note");

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "action.copy" }));
    });
    expect(toastError).toHaveBeenCalledWith("Error: clipboard denied");
    expect(notifyCopied).not.toHaveBeenCalled();
    toastError.mockRestore();
  });
});
