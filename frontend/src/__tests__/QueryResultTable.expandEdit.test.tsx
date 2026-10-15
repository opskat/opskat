import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryResultTable, type CellEdit } from "@/components/query/QueryResultTable";

const { toastWarning, notifyCopied } = vi.hoisted(() => ({ toastWarning: vi.fn(), notifyCopied: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: toastWarning } }));
vi.mock("@/lib/notify", () => ({ notifyCopied, notifySuccess: vi.fn() }));

const columns = ["id", "name", "doc"];
const rows = [
  { id: 1, name: "alice", doc: '{"a":1,"b":[1,2]}' },
  { id: 2, name: null, doc: "plain text" },
];

type GridProps = Partial<React.ComponentProps<typeof QueryResultTable>>;

function renderGrid(props: GridProps = {}) {
  const onCellEdit = vi.fn<(edit: CellEdit) => void>();
  render(
    <QueryResultTable
      columns={columns}
      rows={rows}
      showRowNumber
      editable
      onCellEdit={onCellEdit}
      columnTypes={{ doc: "json" }}
      {...props}
    />
  );
  return { onCellEdit };
}

const grid = () => document.querySelector(".query-table-scroll") as HTMLElement;
const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`) as HTMLElement;
const textarea = () => screen.getByRole("textbox", { name: "query.expandEdit.title" }) as HTMLTextAreaElement;
const openWithShiftEnter = (key: string) => {
  fireEvent.click(cell(key));
  fireEvent.keyDown(grid(), { key: "Enter", shiftKey: true });
};

afterEach(() => {
  cleanup();
  toastWarning.mockReset();
  notifyCopied.mockReset();
});

describe("QueryResultTable expand editor", () => {
  it("Shift+Enter opens the editor with the original text, unformatted, and plain Enter still opens the inline input", () => {
    renderGrid();
    openWithShiftEnter("0:doc");
    expect(textarea().value).toBe('{"a":1,"b":[1,2]}');
    cleanup();
    renderGrid();
    fireEvent.click(cell("0:name"));
    fireEvent.keyDown(grid(), { key: "Enter" });
    expect(screen.queryByRole("textbox", { name: "query.expandEdit.title" })).toBeNull();
  });

  it("opens from the context menu item showing the shortcut hint", () => {
    renderGrid();
    fireEvent.contextMenu(cell("1:doc"), { clientX: 10, clientY: 10 });
    const item = screen.getByRole("menuitem", { name: /query\.expandEdit\.menu/ });
    expect(item.textContent).toMatch(/⇧|Shift/);
    fireEvent.click(item);
    expect(textarea().value).toBe("plain text");
  });

  it("shows the pending edit rather than the stored value", () => {
    renderGrid({ edits: new Map([["0:name", "bob"]]) });
    openWithShiftEnter("0:name");
    expect(textarea().value).toBe("bob");
  });

  it("defaults to JSON mode for object/array values and text mode otherwise", () => {
    renderGrid();
    openWithShiftEnter("0:doc");
    expect(screen.getByRole("radio", { name: "query.expandEdit.json" }).getAttribute("aria-checked")).toBe("true");
    cleanup();
    renderGrid();
    openWithShiftEnter("1:doc");
    expect(screen.getByRole("radio", { name: "query.expandEdit.text" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.queryByRole("button", { name: "query.expandEdit.format" })).toBeNull();
  });

  it("a scalar JSON value such as 123 does not default to JSON mode", () => {
    renderGrid({ rows: [{ id: 1, name: "x", doc: "123" }] });
    openWithShiftEnter("0:doc");
    expect(screen.getByRole("radio", { name: "query.expandEdit.text" }).getAttribute("aria-checked")).toBe("true");
  });

  it("format pretty-prints and compact minifies the JSON", () => {
    renderGrid();
    openWithShiftEnter("0:doc");
    fireEvent.click(screen.getByRole("button", { name: "query.expandEdit.format" }));
    expect(textarea().value).toBe(JSON.stringify({ a: 1, b: [1, 2] }, null, 2));
    fireEvent.click(screen.getByRole("button", { name: "query.expandEdit.compact" }));
    expect(textarea().value).toBe('{"a":1,"b":[1,2]}');
  });

  it("invalid JSON only warns: the validity text shows and OK still records the edit", () => {
    const { onCellEdit } = renderGrid();
    openWithShiftEnter("0:doc");
    expect(screen.getByText("query.expandEdit.jsonValid")).toBeTruthy();
    fireEvent.change(textarea(), { target: { value: '{"a":' } });
    expect(screen.getByText("query.expandEdit.jsonInvalid")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /query\.expandEdit\.ok/ }));
    expect(onCellEdit).toHaveBeenCalledWith({ rowIdx: 0, col: "doc", value: '{"a":' });
  });

  it("Cmd+Enter records the edit through onCellEdit and closes", () => {
    const { onCellEdit } = renderGrid();
    openWithShiftEnter("0:name");
    fireEvent.change(textarea(), { target: { value: "line1\nline2" } });
    fireEvent.keyDown(textarea(), { key: "Enter", metaKey: true });
    expect(onCellEdit).toHaveBeenCalledWith({ rowIdx: 0, col: "name", value: "line1\nline2" });
    expect(screen.queryByRole("textbox", { name: "query.expandEdit.title" })).toBeNull();
  });

  it("unchanged content records nothing, including an untouched NULL cell", () => {
    const { onCellEdit } = renderGrid();
    openWithShiftEnter("0:name");
    fireEvent.click(screen.getByRole("button", { name: /query\.expandEdit\.ok/ }));
    openWithShiftEnter("1:name");
    fireEvent.click(screen.getByRole("button", { name: /query\.expandEdit\.ok/ }));
    expect(onCellEdit).not.toHaveBeenCalled();
  });

  it("Set NULL and Set empty string record the value; Set NULL is disabled for NOT NULL columns", () => {
    const { onCellEdit } = renderGrid({ notNullColumns: new Set(["id"]) });
    openWithShiftEnter("0:name");
    fireEvent.click(screen.getByRole("button", { name: "query.setNull" }));
    expect(onCellEdit).toHaveBeenLastCalledWith({ rowIdx: 0, col: "name", value: null });
    openWithShiftEnter("0:name");
    fireEvent.click(screen.getByRole("button", { name: "query.setEmptyString" }));
    expect(onCellEdit).toHaveBeenLastCalledWith({ rowIdx: 0, col: "name", value: "" });
    openWithShiftEnter("0:id");
    expect((screen.getByRole("button", { name: "query.setNull" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("Esc and Cancel close without edits and return focus to the grid", async () => {
    const { onCellEdit } = renderGrid();
    openWithShiftEnter("0:name");
    fireEvent.change(textarea(), { target: { value: "changed" } });
    fireEvent.keyDown(textarea(), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "query.expandEdit.title" })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(grid()));
    openWithShiftEnter("0:name");
    fireEvent.change(textarea(), { target: { value: "changed" } });
    fireEvent.click(screen.getByRole("button", { name: "action.cancel" }));
    await waitFor(() => expect(document.activeElement).toBe(grid()));
    expect(onCellEdit).not.toHaveBeenCalled();
  });

  it("is read-only on non-editable grids: view and copy only", () => {
    const { onCellEdit } = renderGrid({ editable: false });
    openWithShiftEnter("0:doc");
    expect(textarea().readOnly).toBe(true);
    expect(screen.queryByRole("button", { name: /query\.expandEdit\.ok/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "query.setNull" })).toBeNull();
    expect(screen.queryByRole("button", { name: "query.setEmptyString" })).toBeNull();
    fireEvent.keyDown(textarea(), { key: "Enter", metaKey: true });
    expect(onCellEdit).not.toHaveBeenCalled();
  });

  it("copies the text and confirms through notifyCopied", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderGrid({ editable: false });
    openWithShiftEnter("0:doc");
    fireEvent.click(screen.getByRole("button", { name: "action.copy" }));
    await waitFor(() => expect(notifyCopied).toHaveBeenCalled());
    expect(writeText).toHaveBeenCalledWith('{"a":1,"b":[1,2]}');
  });
});
