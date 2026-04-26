import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableFilterBuilder } from "@/components/query/TableFilterBuilder";
import { createFilterCondition, type TableFilterItem, type TableSortItem } from "@/lib/tableFilter";

describe("TableFilterBuilder", () => {
  it("shows distinct suggested values and writes the selected value into the condition", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();

    render(
      <TableFilterBuilder
        columns={["id", "email"]}
        rows={[
          { id: 1, email: "alice@example.com" },
          { id: 2, email: "11223" },
          { id: 3, email: "alice@example.com" },
        ]}
        filters={[createFilterCondition("f-email", "email")]}
        sorts={[]}
        driver="mysql"
        onChange={onChange}
        onSortsChange={vi.fn()}
        onApply={vi.fn()}
      />
    );

    await user.click(screen.getByTitle("query.chooseFilterValue"));
    const panel = await screen.findByRole("dialog");

    expect(within(panel).getByText("alice@example.com")).toBeInTheDocument();
    expect(within(panel).getByText("11223")).toBeInTheDocument();
    expect(within(panel).getAllByText("alice@example.com")).toHaveLength(1);

    await user.click(within(panel).getByText("11223"));

    expect(onChange).toHaveBeenLastCalledWith([
      expect.objectContaining({ kind: "condition", column: "email", value: "11223" }),
    ]);
  });

  it("adds grouped criteria using the next globally unused column", async () => {
    const user = userEvent.setup();
    let filters: TableFilterItem[] = [createFilterCondition("f-id", "id")];
    const sorts: TableSortItem[] = [];
    const onChange = vi.fn((next: TableFilterItem[]) => {
      filters = next;
    });
    const view = render(
      <TableFilterBuilder
        columns={["id", "email", "name"]}
        rows={[]}
        filters={filters}
        sorts={sorts}
        driver="mysql"
        onChange={onChange}
        onSortsChange={vi.fn()}
        onApply={vi.fn()}
      />
    );

    await user.click(screen.getByTitle("query.addFilterGroup"));
    view.rerender(
      <TableFilterBuilder
        columns={["id", "email", "name"]}
        rows={[]}
        filters={filters}
        sorts={sorts}
        driver="mysql"
        onChange={onChange}
        onSortsChange={vi.fn()}
        onApply={vi.fn()}
      />
    );
    await user.click(screen.getAllByTitle("query.addFilter")[1]);

    const latest = onChange.mock.calls.at(-1)?.[0] as TableFilterItem[];
    const group = latest[1];
    expect(group).toMatchObject({ kind: "group" });
    if (group.kind !== "group") throw new Error("expected group");
    expect(group.items[0]).toMatchObject({ kind: "condition", column: "email" });
  });

  it("adds sort criteria and toggles sort direction from the field suffix control", async () => {
    const user = userEvent.setup();
    let sorts: TableSortItem[] = [];
    const onSortsChange = vi.fn((next: TableSortItem[]) => {
      sorts = next;
    });
    const view = render(
      <TableFilterBuilder
        columns={["id", "email", "name"]}
        rows={[]}
        filters={[]}
        sorts={sorts}
        driver="mysql"
        onChange={vi.fn()}
        onSortsChange={onSortsChange}
        onApply={vi.fn()}
      />
    );

    await user.click(screen.getByTitle("query.addSort"));

    expect(onSortsChange).toHaveBeenLastCalledWith([expect.objectContaining({ column: "id", dir: "asc" })]);

    view.rerender(
      <TableFilterBuilder
        columns={["id", "email", "name"]}
        rows={[]}
        filters={[]}
        sorts={sorts}
        driver="mysql"
        onChange={vi.fn()}
        onSortsChange={onSortsChange}
        onApply={vi.fn()}
      />
    );
    await user.click(screen.getByTitle("query.toggleSortDirection:id"));

    expect(onSortsChange).toHaveBeenLastCalledWith([expect.objectContaining({ column: "id", dir: "desc" })]);
  });
});
