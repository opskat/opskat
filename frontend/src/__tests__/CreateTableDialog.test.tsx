import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { CreateTableDialog } from "@/components/query/CreateTableDialog";
import { ExecuteSQL, GetCreateTableStatements } from "../../wailsjs/go/query/Query";
import { mockSchemaStatements } from "./dbDriverFixtures";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

// 建表语句由驱动生成:预览展示的、确认后执行的,都是 GetCreateTableStatements 给的那条;对话框不认驱动名。
function renderDialog(onSuccess = vi.fn()) {
  render(<CreateTableDialog open onOpenChange={vi.fn()} assetId={1} database="appdb" onSuccess={onSuccess} />);
  return onSuccess;
}

function fillNameAndColumn() {
  fireEvent.change(screen.getByPlaceholderText("query.tableNamePlaceholder"), { target: { value: " users " } });
  fireEvent.change(screen.getByPlaceholderText("query.columnNamePlaceholder"), { target: { value: "id" } });
  fireEvent.change(screen.getByPlaceholderText("query.defaultValuePlaceholder"), { target: { value: "7" } });
}

describe("CreateTableDialog", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: [] }));
    vi.mocked(GetCreateTableStatements).mockReset();
    vi.mocked(toast.error).mockReset();
    mockSchemaStatements();
  });

  it("previews the statement the driver wrote and executes exactly it", async () => {
    const onSuccess = renderDialog();
    fillNameAndColumn();
    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));

    await waitFor(() => expect(screen.getByText("query.sqlPreviewTitle")).toBeInTheDocument());
    expect(GetCreateTableStatements).toHaveBeenCalledWith(1, "appdb", {
      name: "users",
      columns: [{ name: "id", type: "VARCHAR(255)", nullable: true, defaultValue: "7" }],
    });
    const statement =
      'CREATE TABLE appdb.users [{"name":"id","type":"VARCHAR(255)","nullable":true,"defaultValue":"7"}]';
    expect(screen.getByTestId("code-editor")).toHaveTextContent(statement, { normalizeWhitespace: false });

    fireEvent.click(screen.getByText("query.confirmExecute"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith("users"));
    expect(ExecuteSQL).toHaveBeenCalledTimes(1);
    expect(ExecuteSQL).toHaveBeenCalledWith(1, statement, "appdb");
  });

  it("shows the error and opens no preview when the driver cannot write the statement", async () => {
    vi.mocked(GetCreateTableStatements).mockRejectedValue(new Error("unsupported"));
    renderDialog();
    fillNameAndColumn();
    fireEvent.click(screen.getByText("query.designTablePreviewChanges"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: unsupported"));
    expect(screen.queryByText("query.sqlPreviewTitle")).not.toBeInTheDocument();
    expect(ExecuteSQL).not.toHaveBeenCalled();
  });
});
