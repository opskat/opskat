import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryResultTable, type SelectedCellContext } from "@/components/query/QueryResultTable";
import { isMac } from "@/stores/shortcutStore";
import { TABLE_FIND_SCOPE_ATTR } from "@/lib/tableFind";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

const columns = ["id", "customer", "note"];
const rows = [
  { id: 4821, customer: "青木贸易", note: "首单" },
  { id: 4822, customer: "北岭物流", note: null },
  { id: 4823, customer: "云栖科技", note: "转北岭仓发货" },
  { id: 4824, customer: "北岭物流", note: "加急" },
];

type GridProps = Partial<React.ComponentProps<typeof QueryResultTable>>;

function renderGrid(props: GridProps = {}) {
  const onSelectedCellChange = vi.fn<(cell: SelectedCellContext | null) => void>();
  const all: React.ComponentProps<typeof QueryResultTable> = {
    columns,
    rows,
    showRowNumber: true,
    findEnabled: true,
    findScope: { rowCount: rows.length, page: 1 },
    onSelectedCellChange,
    ...props,
  };
  const view = render(<QueryResultTable {...all} />);
  const rerender = (next: GridProps) => view.rerender(<QueryResultTable {...all} {...next} />);
  return { onSelectedCellChange, rerender };
}

const grid = () => document.querySelector(".query-table-scroll") as HTMLElement;
const findInput = () => screen.queryByPlaceholderText("query.find.placeholder") as HTMLInputElement | null;
const lastSelected = (fn: ReturnType<typeof renderGrid>["onSelectedCellChange"]) => fn.mock.calls.at(-1)?.[0];
const currentMark = () => document.querySelector("mark[data-find-current]");
const marks = () => Array.from(document.querySelectorAll("mark")).map((m) => m.closest("td")?.dataset.cellKey);

function pressFind(target: HTMLElement) {
  fireEvent.keyDown(target, { key: "f", code: "KeyF", metaKey: isMac, ctrlKey: !isMac });
}

async function openAndSearch(text: string) {
  pressFind(grid());
  const input = findInput()!;
  fireEvent.change(input, { target: { value: text } });
  return input;
}

describe("QueryResultTable — find in page", () => {
  beforeEach(() => {
    toastError.mockReset();
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("opens a focused find bar on Cmd/Ctrl+F inside the grid", () => {
    renderGrid();
    expect(findInput()).toBeNull();
    expect(grid().closest(`[${TABLE_FIND_SCOPE_ATTR}]`)).not.toBeNull();

    pressFind(grid());

    expect(findInput()).not.toBeNull();
    expect(document.activeElement).toBe(findInput());
    expect(screen.getByText("query.find.scope")).toBeInTheDocument();
    for (const label of [
      "query.find.caseSensitive",
      "query.find.wholeCell",
      "query.find.regex",
      "query.find.previous",
      "query.find.next",
      "query.find.close",
    ]) {
      expect(screen.getByRole("button", { name: label })).toBeInTheDocument();
    }
  });

  it("is not offered by grids that do not opt in (MongoDB results)", () => {
    renderGrid({ findEnabled: undefined, findScope: undefined });

    pressFind(grid());

    expect(findInput()).toBeNull();
    expect(document.querySelector(`[${TABLE_FIND_SCOPE_ATTR}]`)).toBeNull();
  });

  it("selects the first match as the user types, highlights every match and counts them", async () => {
    const { onSelectedCellChange } = renderGrid();
    await openAndSearch("北岭");

    await waitFor(() => expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 1, col: "customer" }));
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    expect(marks()).toEqual(["1:customer", "2:note", "3:customer"]);
    expect(currentMark()?.closest("td")?.dataset.cellKey).toBe("1:customer");
    expect(currentMark()?.textContent).toBe("北岭");
    // the input keeps focus while results are walked
    expect(document.activeElement).toBe(findInput());
  });

  it("Enter / Shift+Enter and the arrow buttons walk the matches and wrap around", async () => {
    const { onSelectedCellChange } = renderGrid();
    const input = await openAndSearch("北岭");
    await waitFor(() => expect(screen.getByText("1 / 3")).toBeInTheDocument());

    fireEvent.keyDown(input, { key: "Enter" });
    expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 2, col: "note" });
    expect(screen.getByText("2 / 3")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "query.find.next" }));
    fireEvent.keyDown(input, { key: "Enter" });
    expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 1, col: "customer" });

    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 3, col: "customer" });
    expect(screen.getByText("3 / 3")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "query.find.previous" }));
    expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 2, col: "note" });
  });

  it("shows 无结果 and marks the input invalid when nothing matches", async () => {
    renderGrid();
    const input = await openAndSearch("华东");

    await waitFor(() => expect(screen.getByText("query.find.noResults")).toBeInTheDocument());
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(marks()).toEqual([]);
  });

  it("flags an invalid regex inline without a toast", async () => {
    renderGrid();
    const input = await openAndSearch("北(");
    fireEvent.click(screen.getByRole("button", { name: "query.find.regex" }));

    await waitFor(() => expect(screen.getByText("query.find.invalidRegex")).toBeInTheDocument());
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(toastError).not.toHaveBeenCalled();
  });

  it("applies the case, whole-cell and regex toggles", async () => {
    renderGrid({ rows: [{ id: 1, customer: "Paid", note: "unpaid" }], columns });
    await openAndSearch("paid");
    await waitFor(() => expect(screen.getByText("1 / 2")).toBeInTheDocument());

    const caseButton = screen.getByRole("button", { name: "query.find.caseSensitive" });
    fireEvent.click(caseButton);
    expect(caseButton).toHaveAttribute("aria-pressed", "true");
    await waitFor(() => expect(marks()).toEqual(["0:note"]));

    fireEvent.click(caseButton);
    fireEvent.click(screen.getByRole("button", { name: "query.find.wholeCell" }));
    await waitFor(() => expect(marks()).toEqual(["0:customer"]));

    fireEvent.click(screen.getByRole("button", { name: "query.find.wholeCell" }));
    fireEvent.change(findInput()!, { target: { value: "^un" } });
    fireEvent.click(screen.getByRole("button", { name: "query.find.regex" }));
    await waitFor(() => expect(marks()).toEqual(["0:note"]));
  });

  it("keeps the keyword and re-runs from the first match when the page changes", async () => {
    const { onSelectedCellChange, rerender } = renderGrid();
    const input = await openAndSearch("北岭");
    await waitFor(() => expect(screen.getByText("1 / 3")).toBeInTheDocument());
    fireEvent.keyDown(input, { key: "Enter" });

    rerender({
      rows: [
        { id: 4900, customer: "北岭物流", note: "北岭" },
        { id: 4901, customer: "松涛家居", note: null },
      ],
    });

    await waitFor(() => expect(screen.getByText("1 / 2")).toBeInTheDocument());
    expect(findInput()?.value).toBe("北岭");
    expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 0, col: "customer" });
  });

  it("recounts after a cell edit without moving the current match", async () => {
    const { onSelectedCellChange, rerender } = renderGrid({ editable: true });
    const input = await openAndSearch("北岭");
    await waitFor(() => expect(screen.getByText("1 / 3")).toBeInTheDocument());
    fireEvent.keyDown(input, { key: "Enter" });
    const callsBefore = onSelectedCellChange.mock.calls.length;

    rerender({ editable: true, edits: new Map<string, unknown>([["0:note", "北岭直发"]]) });

    await waitFor(() => expect(screen.getByText("3 / 4")).toBeInTheDocument());
    expect(onSelectedCellChange.mock.calls.length).toBe(callsBefore);
    expect(marks()).toContain("0:note");
  });

  it("Escape closes the bar, clears the highlight and returns focus to the grid", async () => {
    const { onSelectedCellChange } = renderGrid();
    const input = await openAndSearch("北岭");
    await waitFor(() => expect(screen.getByText("1 / 3")).toBeInTheDocument());

    fireEvent.keyDown(input, { key: "Escape" });

    expect(findInput()).toBeNull();
    expect(marks()).toEqual([]);
    expect(document.activeElement).toBe(grid());
    expect(lastSelected(onSelectedCellChange)).toEqual({ rowIdx: 1, col: "customer" });
  });

  it("reopening keeps the keyword and selects it", async () => {
    renderGrid();
    const input = await openAndSearch("北岭");
    fireEvent.keyDown(input, { key: "Escape" });

    pressFind(grid());

    const reopened = findInput()!;
    expect(reopened.value).toBe("北岭");
    expect(reopened.selectionStart).toBe(0);
    expect(reopened.selectionEnd).toBe(2);
  });

  it("the close button closes the bar as well", async () => {
    const user = userEvent.setup();
    renderGrid();
    await openAndSearch("北岭");

    await user.click(screen.getByRole("button", { name: "query.find.close" }));

    expect(findInput()).toBeNull();
  });

  it("scrolls a match hidden behind a frozen column back into view", async () => {
    const user = userEvent.setup();
    const wide = { a: "x", b: "target", c: "y" };
    renderGrid({ columns: ["a", "b", "c"], rows: [wide] });
    fireEvent.contextMenu(document.querySelector('[data-column-header-key="a"]') as HTMLElement, {
      clientX: 40,
      clientY: 20,
    });
    await user.click(screen.getByText("query.freezeColumn"));

    // gutter 44px + frozen "a" 160px → anything left of x=204 sits under the frozen area
    const container = grid();
    Object.defineProperty(container, "clientWidth", { configurable: true, value: 600 });
    container.scrollLeft = 300;
    const rect = (left: number, width: number) =>
      ({
        left,
        right: left + width,
        top: 0,
        bottom: 30,
        width,
        height: 30,
        x: left,
        y: 0,
        toJSON: () => ({}),
      }) as DOMRect;
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
      if (this.getAttribute("data-cell-key") === "0:b") return rect(100, 160);
      return rect(0, 600);
    });

    await openAndSearch("target");

    await waitFor(() => expect(container.scrollLeft).toBe(196));
  });
});
