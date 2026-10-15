import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryResultTable, type CellEdit, type SelectedCellContext } from "@/components/query/QueryResultTable";
import { formatModKey } from "@/stores/shortcutStore";

const { toastWarning } = vi.hoisted(() => ({ toastWarning: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: toastWarning } }));

const columns = ["id", "name", "note"];
const rows = Array.from({ length: 8 }, (_, i) => ({ id: i + 1, name: `n${i + 1}`, note: `t${i + 1}` }));

type GridProps = Partial<React.ComponentProps<typeof QueryResultTable>>;

function renderGrid(props: GridProps = {}) {
  const onSelectedCellChange = vi.fn<(cell: SelectedCellContext | null) => void>();
  const onSelectedRowsChange = vi.fn<(rows: number[]) => void>();
  const onCellEdit = vi.fn<(edit: CellEdit) => void>();
  const view = render(
    <QueryResultTable
      columns={columns}
      rows={rows}
      showRowNumber
      onSelectedCellChange={onSelectedCellChange}
      onSelectedRowsChange={onSelectedRowsChange}
      onCellEdit={onCellEdit}
      {...props}
    />
  );
  const rerender = (next: GridProps) =>
    view.rerender(
      <QueryResultTable
        columns={columns}
        rows={rows}
        showRowNumber
        onSelectedCellChange={onSelectedCellChange}
        onCellEdit={onCellEdit}
        {...props}
        {...next}
      />
    );
  return { onSelectedCellChange, onSelectedRowsChange, onCellEdit, rerender };
}

const grid = () => document.querySelector(".query-table-scroll") as HTMLElement;
const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`) as HTMLElement;
const key = (k: string, init: KeyboardEventInit = {}) => fireEvent.keyDown(grid(), { key: k, ...init });
const mod = { metaKey: true, ctrlKey: true };
const last = (fn: { mock: { calls: unknown[][] } }) => fn.mock.calls.at(-1)?.[0];

function selectAt(rowIdx: number, col: string, onChange?: ReturnType<typeof renderGrid>) {
  fireEvent.click(cell(`${rowIdx}:${col}`));
  return onChange;
}

afterEach(() => {
  cleanup();
  toastWarning.mockReset();
});

describe("QueryResultTable keyboard — Tab / Home / End / Page", () => {
  it("Tab moves right and wraps to the first column of the next row", () => {
    const g = renderGrid();
    selectAt(0, "name");
    key("Tab");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 0, col: "note" });
    key("Tab");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 1, col: "id" });
  });

  it("Shift+Tab moves left and wraps to the last column of the previous row", () => {
    const g = renderGrid();
    selectAt(1, "name");
    key("Tab", { shiftKey: true });
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 1, col: "id" });
    key("Tab", { shiftKey: true });
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 0, col: "note" });
  });

  it("Tab at the very last cell leaves focus movement to the browser", () => {
    const g = renderGrid();
    selectAt(7, "note");
    const notPrevented = fireEvent.keyDown(grid(), { key: "Tab" });
    expect(notPrevented).toBe(true);
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 7, col: "note" });
  });

  it("Home / End jump to the first / last column of the row", () => {
    const g = renderGrid();
    selectAt(3, "name");
    key("End");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 3, col: "note" });
    key("Home");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 3, col: "id" });
  });

  it("Mod+Home / Mod+End jump to the first row first column / last row last column", () => {
    const g = renderGrid();
    selectAt(4, "name");
    key("End", mod);
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 7, col: "note" });
    key("Home", mod);
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 0, col: "id" });
  });

  it("PageDown / PageUp move by the number of visible rows", () => {
    const g = renderGrid();
    // 28px estimated rows (default density); room for exactly 3 of them.
    Object.defineProperty(grid(), "clientHeight", { configurable: true, value: 84 });
    selectAt(1, "name");
    key("PageDown");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 4, col: "name" });
    key("PageDown");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 7, col: "name" });
    key("PageDown");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 7, col: "name" });
    key("PageUp");
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 4, col: "name" });
  });
});

describe("QueryResultTable keyboard — select all", () => {
  it("Mod+A selects every row of the page, like the row-number corner", () => {
    const g = renderGrid();
    selectAt(2, "name");
    const notPrevented = fireEvent.keyDown(grid(), { key: "a", ...mod });
    expect(notPrevented).toBe(false);
    expect(last(g.onSelectedRowsChange)).toEqual(rows.map((_, i) => i));
  });

  it("Mod+A twice keeps everything selected instead of toggling off", () => {
    const g = renderGrid();
    key("a", mod);
    key("a", mod);
    expect(last(g.onSelectedRowsChange)).toEqual(rows.map((_, i) => i));
  });
});

describe("QueryResultTable keyboard — Delete / Backspace sets NULL", () => {
  it("records a NULL edit through onCellEdit in an editable grid", () => {
    const g = renderGrid({ editable: true });
    selectAt(2, "name");
    key("Delete");
    expect(g.onCellEdit).toHaveBeenLastCalledWith({ rowIdx: 2, col: "name", value: null });
    key("Backspace");
    expect(g.onCellEdit).toHaveBeenCalledTimes(2);
  });

  it("does nothing in a read-only grid", () => {
    const g = renderGrid();
    selectAt(2, "name");
    key("Delete");
    expect(g.onCellEdit).not.toHaveBeenCalled();
    expect(toastWarning).not.toHaveBeenCalled();
  });

  it("warns and changes nothing for a NOT NULL column", () => {
    const g = renderGrid({ editable: true, notNullColumns: new Set(["name"]) });
    selectAt(2, "name");
    key("Delete");
    expect(g.onCellEdit).not.toHaveBeenCalled();
    expect(toastWarning).toHaveBeenCalledTimes(1);
    selectAt(2, "note");
    key("Delete");
    expect(g.onCellEdit).toHaveBeenCalledWith({ rowIdx: 2, col: "note", value: null });
  });

  it("is inert while the grid reloads", () => {
    const g = renderGrid({ editable: true });
    selectAt(2, "name");
    g.rerender({ keepShapeWhileLoading: true, loading: true });
    key("Delete");
    expect(g.onCellEdit).not.toHaveBeenCalled();
  });
});

describe("QueryResultTable keyboard — inert contexts", () => {
  it("does nothing while a cell is being edited", () => {
    const g = renderGrid({ editable: true });
    selectAt(2, "name");
    key("F2");
    g.onSelectedRowsChange.mockClear();
    const before = g.onSelectedCellChange.mock.calls.length;
    key("Delete");
    key("Tab");
    key("End");
    key("a", mod);
    expect(g.onCellEdit).not.toHaveBeenCalled();
    expect(g.onSelectedCellChange.mock.calls.length).toBe(before);
    expect(g.onSelectedRowsChange).not.toHaveBeenCalled();
  });

  it("ignores keys that come from an input inside the grid", () => {
    const g = renderGrid({ editable: true });
    selectAt(2, "name");
    const input = document.createElement("input");
    grid().appendChild(input);
    g.onSelectedRowsChange.mockClear();
    fireEvent.keyDown(input, { key: "Delete" });
    fireEvent.keyDown(input, { key: "Tab" });
    fireEvent.keyDown(input, { key: "a", ...mod });
    expect(g.onCellEdit).not.toHaveBeenCalled();
    expect(g.onSelectedRowsChange).not.toHaveBeenCalled();
    expect(last(g.onSelectedCellChange)).toEqual({ rowIdx: 2, col: "name" });
  });
});

describe("QueryResultTable keyboard — context menu hints", () => {
  it("shows Delete next to Set NULL and the copy shortcut next to Copy", () => {
    renderGrid({ editable: true });
    fireEvent.contextMenu(cell("1:name"), { clientX: 40, clientY: 50 });
    const nullItem = screen.getByText("query.setNull").closest("button")!;
    expect(nullItem.textContent).toContain("Delete");
    const copyItem = screen.getByText("query.copyValue").closest("button")!;
    expect(copyItem.textContent).toContain(formatModKey("KeyC"));
  });
});
