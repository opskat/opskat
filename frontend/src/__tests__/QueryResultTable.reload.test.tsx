import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryResultTable } from "@/components/query/QueryResultTable";
import { isMac } from "@/stores/shortcutStore";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

type GridProps = React.ComponentProps<typeof QueryResultTable>;

const rowsPage1 = [
  { id: 1, name: "青木贸易" },
  { id: 2, name: "北岭物流" },
];
const rowsPage2 = [
  { id: 3, name: "云栖科技" },
  { id: 4, name: "北岭物流" },
  { id: 5, name: "北岭仓" },
];

function renderGrid(props: Partial<GridProps> = {}) {
  const base: GridProps = {
    columns: ["id", "name"],
    rows: rowsPage1,
    columnTypes: { id: "int", name: "varchar(64)" },
    showRowNumber: true,
    keepShapeWhileLoading: true,
    ...props,
  };
  const view = render(<QueryResultTable {...base} />);
  // 每次重渲染都给一份新的列数组 —— 与 TableDataTab / SqlEditorTab 每次取数后 setColumns(新数组) 一致。
  const rerender = (next: Partial<GridProps>) =>
    view.rerender(<QueryResultTable {...base} columns={[...(next.columns ?? base.columns)]} {...next} />);
  return { rerender };
}

const header = (col: string) => document.querySelector(`[data-column-header-key="${col}"]`) as HTMLElement | null;
const scroller = () => document.querySelector(".query-table-scroll") as HTMLElement | null;

function columnMenu(col: string, item: string) {
  fireEvent.contextMenu(header(col)!, { clientX: 40, clientY: 20 });
  fireEvent.click(screen.getByText(item));
}

function setWidth(col: string, width: number) {
  Object.defineProperty(window, "prompt", { configurable: true, value: vi.fn().mockReturnValue(String(width)) });
  columnMenu(col, "query.setColumnWidth");
}

describe("QueryResultTable — reload keeps the grid", () => {
  beforeEach(() => cleanup());
  afterEach(() => vi.restoreAllMocks());

  it("keeps a resized column width through a refresh that returns the same columns", () => {
    const { rerender } = renderGrid();
    setWidth("name", 220);
    expect(header("name")!.style.width).toBe("220px");

    rerender({ loading: true });
    expect(header("name")!.style.width).toBe("220px");

    rerender({ loading: false, rows: rowsPage2 });
    expect(header("name")!.style.width).toBe("220px");
  });

  it("keeps the header, scroll position and old rows while reloading, with a progress bar", () => {
    const { rerender } = renderGrid();
    const container = scroller()!;
    container.scrollTop = 120;
    container.scrollLeft = 40;

    rerender({ loading: true });

    expect(scroller()).toBe(container);
    expect(container.scrollTop).toBe(120);
    expect(container.scrollLeft).toBe(40);
    expect(header("id")).toBeTruthy();
    expect(document.querySelector('[data-cell-key="1:name"]')?.textContent).toBe("北岭物流");
    expect(container).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("progressbar")).toBeInTheDocument();

    rerender({ loading: false, rows: rowsPage2 });
    expect(container).not.toHaveAttribute("aria-busy", "true");
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(document.querySelector('[data-cell-key="2:name"]')?.textContent).toBe("北岭仓");
  });

  it("does not let old rows be edited while reloading", () => {
    const onCellEdit = vi.fn();
    const { rerender } = renderGrid({ editable: true, onCellEdit });
    rerender({ editable: true, onCellEdit, loading: true });

    const cell = document.querySelector('[data-cell-key="0:name"]') as HTMLElement;
    fireEvent.doubleClick(cell);
    expect(cell.querySelector("input")).toBeNull();

    fireEvent.click(cell);
    fireEvent.keyDown(scroller()!, { key: "Enter" });
    expect(cell.querySelector("input")).toBeNull();
  });

  it("restores the normal grid when loading stops without new data", () => {
    const { rerender } = renderGrid({ editable: true });
    rerender({ editable: true, loading: true });
    rerender({ editable: true, loading: false });

    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(scroller()).not.toHaveAttribute("aria-busy", "true");
    const cell = document.querySelector('[data-cell-key="0:name"]') as HTMLElement;
    fireEvent.doubleClick(cell);
    expect(cell.querySelector("input")).toBeTruthy();
  });

  it("still shows a centred spinner on the first load, before any columns exist", () => {
    renderGrid({ columns: [], rows: [], loading: true });
    expect(scroller()).toBeNull();
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(document.querySelector(".animate-spin")).toBeTruthy();
  });

  it("grids that do not opt in (MongoDB results) still swap to a spinner while loading", () => {
    const { rerender } = renderGrid({ keepShapeWhileLoading: false });
    rerender({ keepShapeWhileLoading: false, loading: true });
    expect(scroller()).toBeNull();
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(document.querySelector(".animate-spin")).toBeTruthy();
  });

  it("keeps the find keyword across a reload and recounts against the new rows", async () => {
    const { rerender } = renderGrid({ findEnabled: true, findScope: { rowCount: 2, page: 1 } });
    fireEvent.keyDown(scroller()!, { key: "f", code: "KeyF", metaKey: isMac, ctrlKey: !isMac });
    fireEvent.change(screen.getByPlaceholderText("query.find.placeholder"), { target: { value: "北岭" } });
    await waitFor(() => expect(screen.getByText("1 / 1")).toBeInTheDocument());

    rerender({ findEnabled: true, findScope: { rowCount: 2, page: 1 }, loading: true });
    expect((screen.getByPlaceholderText("query.find.placeholder") as HTMLInputElement).value).toBe("北岭");

    rerender({ findEnabled: true, findScope: { rowCount: 3, page: 2 }, rows: rowsPage2 });
    await waitFor(() => expect(screen.getByText("1 / 2")).toBeInTheDocument());
    expect((screen.getByPlaceholderText("query.find.placeholder") as HTMLInputElement).value).toBe("北岭");
  });
});

describe("QueryResultTable — column state follows the ordered column names", () => {
  beforeEach(() => cleanup());
  afterEach(() => vi.restoreAllMocks());

  const frozen = (col: string) => header(col)!.classList.contains("sticky");
  const typeShown = () => screen.queryByText("varchar(64)") !== null;

  it("keeps widths, frozen columns and the type / comment toggles when the names are unchanged", () => {
    const { rerender } = renderGrid();
    setWidth("name", 220);
    columnMenu("id", "query.freezeColumn");
    columnMenu("id", "query.showFieldType");
    columnMenu("id", "query.showComment");
    expect(frozen("id")).toBe(true);
    expect(typeShown()).toBe(false);
    expect(screen.getAllByText("query.noComment")).toHaveLength(2);

    rerender({ rows: rowsPage2 });

    expect(header("name")!.style.width).toBe("220px");
    expect(frozen("id")).toBe(true);
    expect(typeShown()).toBe(false);
    expect(screen.getAllByText("query.noComment")).toHaveLength(2);
  });

  it("resets them when the ordered column names change", () => {
    const { rerender } = renderGrid();
    setWidth("name", 220);
    columnMenu("id", "query.freezeColumn");
    columnMenu("id", "query.showFieldType");
    columnMenu("id", "query.showComment");

    rerender({ columns: ["name", "id"], rows: rowsPage2 });

    expect(header("name")!.style.width).toBe("160px");
    expect(frozen("id")).toBe(false);
    expect(typeShown()).toBe(true);
    expect(screen.queryByText("query.noComment")).toBeNull();
  });
});
