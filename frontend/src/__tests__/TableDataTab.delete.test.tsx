import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fireEvent } from "@testing-library/react";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQL, OpenTable } from "../../wailsjs/go/query/Query";

function openTablePayload(rows: Record<string, unknown>[]) {
  return JSON.stringify({
    columns: ["id", "name"],
    columnTypes: {},
    columnRules: [],
    primaryKeys: ["id"],
    totalCount: rows.length,
    firstPage: rows,
    pageSize: 1000,
  });
}

function setupStores() {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "db",
          assetIcon: "",
          assetType: "database",
          driver: "mysql",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["users"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "users" }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
}

const ROWS = [
  { id: 1, name: "ada" },
  { id: 2, name: "bob" },
  { id: 3, name: "cy" },
];

const gutter = (origIdx: number) => document.querySelector(`[data-row-header-key="${origIdx}"]`) as HTMLElement;

const deleteCalls = () =>
  vi
    .mocked(ExecuteSQL)
    .mock.calls.map(([, sql]) => String(sql))
    .filter((sql) => sql.startsWith("DELETE"));

async function renderLoaded(rows = ROWS) {
  vi.mocked(OpenTable).mockResolvedValue(openTablePayload(rows));
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
  await waitFor(() => expect(gutter(0)).toBeTruthy());
}

describe("TableDataTab multi-row delete", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(OpenTable).mockReset();
    setupStores();
  });

  it("previews and executes one DELETE per selected row", async () => {
    const user = userEvent.setup();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    await renderLoaded();

    fireEvent.click(gutter(0));
    fireEvent.click(gutter(2), { ctrlKey: true });
    fireEvent.contextMenu(gutter(2), { clientX: 20, clientY: 40 });
    await user.click(screen.getByText("query.deleteRecords"));

    // Nothing runs until the preview is confirmed.
    expect(deleteCalls()).toHaveLength(0);

    await user.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => expect(deleteCalls()).toHaveLength(2));
    expect(deleteCalls()[0]).toContain("`id` = '1'");
    expect(deleteCalls()[1]).toContain("`id` = '3'");
  });

  it("surfaces per-statement failures without dropping the rest", async () => {
    const user = userEvent.setup();
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).includes("`id` = '1'")) throw new Error("lock wait timeout");
      return JSON.stringify({ affected_rows: 1 });
    });
    await renderLoaded();

    fireEvent.click(gutter(0));
    fireEvent.click(gutter(1), { ctrlKey: true });
    fireEvent.contextMenu(gutter(1), { clientX: 20, clientY: 40 });
    await user.click(screen.getByText("query.deleteRecords"));
    await user.click(screen.getByText("query.confirmExecute"));

    // The failing statement does not abort the second one.
    await waitFor(() => expect(deleteCalls()).toHaveLength(2));
  });

  it("removes an unsaved new row locally instead of generating SQL for it", async () => {
    const user = userEvent.setup();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    await renderLoaded();

    await user.click(screen.getByTitle("query.addRow"));
    await waitFor(() => expect(gutter(3)).toBeTruthy());

    fireEvent.click(gutter(3));
    fireEvent.contextMenu(gutter(3), { clientX: 20, clientY: 40 });
    await user.click(screen.getByText("query.deleteRecord"));

    // No preview, no statement — the row only ever existed in the client.
    await waitFor(() => expect(gutter(3)).toBeNull());
    expect(screen.queryByText("query.confirmExecute")).not.toBeInTheDocument();
    expect(deleteCalls()).toHaveLength(0);
  });

  it("enables the status bar delete action for the whole row selection", async () => {
    await renderLoaded();
    const deleteAction = screen.getByTitle(/^query\.deleteRecord/);
    expect(deleteAction).toBeDisabled();

    fireEvent.click(gutter(0));
    fireEvent.click(gutter(1), { ctrlKey: true });

    await waitFor(() => expect(screen.getByTitle("query.deleteRecords")).toBeEnabled());
  });
});
