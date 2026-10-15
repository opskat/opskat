import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryResultTable } from "@/components/query/QueryResultTable";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

type GridProps = React.ComponentProps<typeof QueryResultTable>;
type ColumnMeta = NonNullable<GridProps["columnMeta"]>;

const columnMeta: ColumnMeta = {
  id: { comment: "订单编号", type: "bigint", nullable: false, primaryKey: true, indexes: ["PRIMARY", "idx_id_name"] },
  name: { comment: "", type: "varchar(64)", nullable: true, primaryKey: false, default: "", indexes: [] },
  status: { comment: "状态", type: "int", nullable: false, primaryKey: false, default: "0", indexes: [] },
  note: { comment: "", type: "text", nullable: true, primaryKey: false, indexes: [] },
};

function renderGrid(props: Partial<GridProps> = {}) {
  return render(
    <QueryResultTable
      columns={["id", "name", "status", "note"]}
      rows={[{ id: 1, name: "青木贸易", status: 0, note: null }]}
      columnTypes={{ id: "bigint", name: "varchar(64)", status: "int", note: "text" }}
      {...props}
    />
  );
}

const header = (col: string) => document.querySelector(`[data-column-header-key="${col}"]`) as HTMLElement;
const nameOf = (col: string) => header(col).querySelector("span.truncate") as HTMLElement;

function toggleComments() {
  fireEvent.contextMenu(header("id"), { clientX: 40, clientY: 20 });
  fireEvent.click(screen.getByText("query.showComment"));
}

describe("QueryResultTable — column comments and header card", () => {
  beforeEach(() => cleanup());
  afterEach(() => vi.restoreAllMocks());

  it("shows the real comment under each column name, and a dash when there is none", () => {
    renderGrid({ columnMeta });
    toggleComments();
    expect(header("id")).toHaveTextContent("订单编号");
    expect(header("status")).toHaveTextContent("状态");
    expect(header("name")).toHaveTextContent("—");
    expect(header("note")).toHaveTextContent("—");
    expect(screen.queryByText("query.noComment")).toBeNull();
  });

  it("keeps the comment toggle across a reload with the same column names", () => {
    const view = renderGrid({ columnMeta });
    toggleComments();
    view.rerender(
      <QueryResultTable
        columns={["id", "name", "status", "note"]}
        rows={[{ id: 2, name: "x", status: 1, note: null }]}
        columnMeta={columnMeta}
      />
    );
    expect(header("id")).toHaveTextContent("订单编号");
  });

  it("offers no comment toggle without column metadata (SQL results, metadata failure)", () => {
    renderGrid();
    fireEvent.contextMenu(header("id"), { clientX: 40, clientY: 20 });
    expect(screen.getByText("query.showFieldType")).toBeInTheDocument();
    expect(screen.queryByText("query.showComment")).toBeNull();
    expect(nameOf("id")).toHaveTextContent("id");
  });

  it("opens the info card only after a hover delay and closes it on leave without taking focus", async () => {
    const user = userEvent.setup();
    renderGrid({ columnMeta });
    const focused = document.activeElement;

    await user.hover(nameOf("id"));
    expect(screen.queryByRole("tooltip")).toBeNull();

    const card = await screen.findByRole("tooltip", {}, { timeout: 2000 });
    expect(card).toHaveTextContent("id");
    expect(card).toHaveTextContent("bigint");
    expect(card).toHaveTextContent("query.columnInfoNotNull");
    expect(card).toHaveTextContent("query.columnInfoPrimaryKey");
    expect(card).toHaveTextContent("订单编号");
    expect(card).toHaveTextContent("PRIMARY");
    expect(card).toHaveTextContent("idx_id_name");
    expect(card).not.toHaveTextContent("query.columnInfoDefault");
    expect(document.activeElement).toBe(focused);

    fireEvent.pointerLeave(nameOf("id"));
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull());
  });

  it("omits the default row when there is none, and shows an empty-string default visibly", async () => {
    const user = userEvent.setup();
    renderGrid({ columnMeta });

    await user.hover(nameOf("note"));
    let card = await screen.findByRole("tooltip", {}, { timeout: 2000 });
    expect(card).toHaveTextContent("query.columnInfoNullable");
    expect(card).not.toHaveTextContent("query.columnInfoDefault");
    expect(card).not.toHaveTextContent("query.columnInfoComment");
    fireEvent.pointerLeave(nameOf("note"));
    await waitFor(() => expect(screen.queryByRole("tooltip")).toBeNull());

    await user.hover(nameOf("name"));
    card = await screen.findByRole("tooltip", {}, { timeout: 2000 });
    expect(card).toHaveTextContent("query.columnInfoDefault");
    expect(card).toHaveTextContent("''");
  });

  it("shows no card without column metadata", async () => {
    const user = userEvent.setup();
    renderGrid();
    await user.hover(nameOf("id"));
    await new Promise((r) => setTimeout(r, 800));
    expect(screen.queryByRole("tooltip")).toBeNull();
  });
});
