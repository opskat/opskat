import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { AlterTableDialog } from "@/components/query/AlterTableDialog";
import { ExecuteSQL, GetAlterTableModel, GetAlterTableStatements } from "../../wailsjs/go/query/Query";
import { dbdriver } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

// 改表对话框:表的样子由驱动读出,语句由驱动生成(预览展示的、确认后逐条执行的都是驱动给的),哪些输入
// 可改按驱动描述的 alterTableAttributes 锁定;对话框不认驱动名。

const MODEL = dbdriver.AlterTableModel.createFrom({
  name: "events",
  comment: "event log",
  columns: [
    { name: "id", type: "integer", nullable: false, defaultValue: "", comment: "pk" },
    { name: "status", type: "event_status", nullable: true, defaultValue: "'new'::event_status", comment: "" },
  ],
});

function renderDialog(driver: string, table = "reporting.events", onSuccess = vi.fn()) {
  render(
    <AlterTableDialog
      open
      onOpenChange={vi.fn()}
      assetId={1}
      database="appdb"
      table={table}
      driver={driver}
      onSuccess={onSuccess}
    />
  );
  return onSuccess;
}

const inputs = (testid: string) => screen.getAllByTestId(testid) as HTMLInputElement[];

async function loaded() {
  await waitFor(() => expect(inputs("alter-column-name")).toHaveLength(MODEL.columns.length));
}

describe("AlterTableDialog", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 0 }));
    vi.mocked(GetAlterTableModel).mockReset();
    vi.mocked(GetAlterTableModel).mockResolvedValue(MODEL);
    vi.mocked(GetAlterTableStatements).mockReset();
    vi.mocked(GetAlterTableStatements).mockResolvedValue(
      dbdriver.AlterTableStatements.createFrom({ statements: ["ALTER ONE", "ALTER TWO"], table: "reporting.events_v2" })
    );
    vi.mocked(toast.error).mockReset();
    provideDrivers(BUILTIN_DRIVERS);
  });

  it("shows the table the driver read, with the name to rename without its schema", async () => {
    renderDialog("postgresql");
    await loaded();

    expect(GetAlterTableModel).toHaveBeenCalledWith(1, "appdb", "reporting.events");
    expect(ExecuteSQL).not.toHaveBeenCalled();
    expect((screen.getByTestId("alter-table-name") as HTMLInputElement).value).toBe("events");
    expect((screen.getByTestId("alter-table-comment") as HTMLInputElement).value).toBe("event log");
    expect(inputs("alter-column-name").map((i) => i.value)).toEqual(["id", "status"]);
    expect(inputs("alter-column-type").map((i) => i.value)).toEqual(["integer", "event_status"]);
    expect(inputs("alter-column-default").map((i) => i.value)).toEqual(["", "'new'::event_status"]);
    expect(inputs("alter-column-comment").map((i) => i.value)).toEqual(["pk", ""]);
    expect(screen.getByTestId("alter-preview")).toBeDisabled();
  });

  it("sends the edited table to the driver, previews its statements and executes exactly those in order", async () => {
    const onSuccess = renderDialog("postgresql");
    await loaded();

    fireEvent.change(screen.getByTestId("alter-table-name"), { target: { value: " events_v2 " } });
    fireEvent.change(inputs("alter-column-name")[1], { target: { value: "state" } });
    fireEvent.click(screen.getByTestId("alter-add-column"));
    fireEvent.change(inputs("alter-column-name")[2], { target: { value: "note" } });
    fireEvent.click(screen.getAllByTitle("query.removeColumn")[0]);
    fireEvent.click(screen.getByTestId("alter-preview"));

    await waitFor(() => expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument());
    expect(GetAlterTableStatements).toHaveBeenCalledWith(1, "appdb", "reporting.events", {
      name: "events_v2",
      comment: "event log",
      columns: [
        {
          originalName: "status",
          name: "state",
          type: "event_status",
          nullable: true,
          defaultValue: "'new'::event_status",
          comment: "",
        },
        { originalName: "", name: "note", type: "VARCHAR(255)", nullable: true, defaultValue: "", comment: "" },
      ],
      drops: ["id"],
    });
    expect(screen.getByTestId("code-editor").textContent).toContain("ALTER ONE");
    expect(screen.getByTestId("code-editor").textContent).toContain("ALTER TWO");
    expect(ExecuteSQL).not.toHaveBeenCalled();

    fireEvent.click(screen.getByText("query.confirmExecute"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith("reporting.events_v2"));
    expect(vi.mocked(ExecuteSQL).mock.calls).toEqual([
      [1, "ALTER ONE", "appdb"],
      [1, "ALTER TWO", "appdb"],
    ]);
  });

  it("does not reopen the table under a new name when it was not renamed", async () => {
    vi.mocked(GetAlterTableStatements).mockResolvedValue(
      dbdriver.AlterTableStatements.createFrom({ statements: ["ALTER ONE"], table: "reporting.events" })
    );
    const onSuccess = renderDialog("postgresql");
    await loaded();
    fireEvent.change(inputs("alter-column-comment")[1], { target: { value: "state" } });
    fireEvent.click(screen.getByTestId("alter-preview"));
    await waitFor(() => expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("query.confirmExecute"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(undefined));
  });

  it("stops at the first statement that fails", async () => {
    vi.mocked(ExecuteSQL).mockRejectedValueOnce(new Error("boom"));
    const onSuccess = renderDialog("postgresql");
    await loaded();
    fireEvent.change(screen.getByTestId("alter-table-name"), { target: { value: "events_v2" } });
    fireEvent.click(screen.getByTestId("alter-preview"));
    await waitFor(() => expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: boom"));
    expect(ExecuteSQL).toHaveBeenCalledTimes(1);
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("shows the error, opens no preview and executes nothing when the driver cannot write the statements", async () => {
    vi.mocked(GetAlterTableStatements).mockRejectedValue(new Error("column type is locked"));
    renderDialog("postgresql");
    await loaded();
    fireEvent.change(screen.getByTestId("alter-table-name"), { target: { value: "events_v2" } });
    fireEvent.click(screen.getByTestId("alter-preview"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: column type is locked"));
    expect(screen.queryByText("query.sqlPreviewTitle")).not.toBeInTheDocument();
    expect(ExecuteSQL).not.toHaveBeenCalled();
  });

  it("says there is nothing to change when the driver writes no statement", async () => {
    vi.mocked(GetAlterTableStatements).mockResolvedValue(
      dbdriver.AlterTableStatements.createFrom({ statements: [], table: "reporting.events" })
    );
    renderDialog("postgresql");
    await loaded();
    fireEvent.change(inputs("alter-column-type")[0], { target: { value: "bigint" } });
    fireEvent.click(screen.getByTestId("alter-preview"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("query.designTableNoChanges"));
    expect(screen.queryByText("query.sqlPreviewTitle")).not.toBeInTheDocument();
  });

  it("shows the load error where it did before", async () => {
    vi.mocked(GetAlterTableModel).mockRejectedValue(new Error("table reporting.events not found"));
    renderDialog("postgresql");
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: table reporting.events not found"));
    expect(screen.queryAllByTestId("alter-column-name")).toHaveLength(0);
  });

  // 锁定范围按驱动描述:SQLite 的已有列只能改名或删除、没有注释;SQL Server 的已有列不能改默认值,
  // 列与表注释都不能改;MySQL 全部可改。说明文字随声明而来。
  it("locks what a driver that declares no alterable attribute cannot change (SQLite)", async () => {
    renderDialog("sqlite", "events");
    await loaded();
    fireEvent.click(screen.getByTestId("alter-add-column"));

    const disabled = (testid: string) => inputs(testid).map((i) => i.disabled);
    expect(disabled("alter-column-name")).toEqual([false, false, false]);
    expect(disabled("alter-column-type")).toEqual([true, true, false]);
    expect(disabled("alter-column-default")).toEqual([true, true, false]);
    expect(disabled("alter-column-comment")).toEqual([true, true, true]);
    expect(screen.getAllByRole("switch").map((s) => (s as HTMLButtonElement).disabled)).toEqual([true, true, false]);
    expect(screen.getByTestId("alter-table-comment")).toBeDisabled();
    expect(screen.getByTestId("alter-table-comment")).toHaveAttribute(
      "placeholder",
      "query.alterTableCommentUnsupported"
    );
    expect(screen.getByText("query.alterLimitsRenameAddDropOnly")).toBeInTheDocument();
    expect(screen.queryByText("query.alterLimitsTypeAndNullabilityOnly")).not.toBeInTheDocument();
  });

  it("locks defaults and comments for a driver that declares only type and nullability (SQL Server)", async () => {
    renderDialog("mssql", "dbo.events");
    await loaded();
    fireEvent.click(screen.getByTestId("alter-add-column"));

    const disabled = (testid: string) => inputs(testid).map((i) => i.disabled);
    expect(disabled("alter-column-type")).toEqual([false, false, false]);
    expect(disabled("alter-column-default")).toEqual([true, true, false]);
    expect(disabled("alter-column-comment")).toEqual([true, true, true]);
    expect(screen.getAllByRole("switch").map((s) => (s as HTMLButtonElement).disabled)).toEqual([false, false, false]);
    expect(screen.getByTestId("alter-table-comment")).toBeDisabled();
    expect(screen.getByTestId("alter-table-comment")).toHaveAttribute(
      "placeholder",
      "query.alterTableCommentExtendedProperties"
    );
    expect(screen.getByText("query.alterLimitsTypeAndNullabilityOnly")).toBeInTheDocument();
  });

  it("locks nothing for a driver that declares every attribute (MySQL)", async () => {
    renderDialog("mysql", "events");
    await loaded();

    for (const testid of ["alter-column-type", "alter-column-default", "alter-column-comment"]) {
      expect(inputs(testid).every((i) => !i.disabled)).toBe(true);
    }
    expect(screen.getByTestId("alter-table-comment")).toBeEnabled();
    expect(screen.queryByText("query.alterLimitsRenameAddDropOnly")).not.toBeInTheDocument();
    expect(screen.queryByText("query.alterLimitsTypeAndNullabilityOnly")).not.toBeInTheDocument();
  });

  it("follows the declaration of a driver the dialog has never heard of", async () => {
    provideDrivers([
      new dbdriver.Descriptor({ ...fakeDriverWith(["alter_table"]), alterTableAttributes: ["column_default"] }),
    ]);
    renderDialog("fakedb", "events");
    await loaded();

    expect(inputs("alter-column-type").every((i) => i.disabled)).toBe(true);
    expect(inputs("alter-column-default").every((i) => !i.disabled)).toBe(true);
    expect(screen.getByTestId("alter-table-comment")).toBeDisabled();
    expect(screen.queryByText("query.alterLimitsRenameAddDropOnly")).not.toBeInTheDocument();
    expect(screen.queryByText("query.alterLimitsTypeAndNullabilityOnly")).not.toBeInTheDocument();
  });
});
