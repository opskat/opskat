import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SqlEditorTab } from "@/components/query/SqlEditorTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQLPaged } from "../../wailsjs/go/query/Query";
import { isMac } from "@/stores/shortcutStore";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", async () => ({
  CodeEditor: (await import("../components/query/__tests__/FakeCodeEditor")).FakeCodeEditor,
}));

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
        innerTabs: [{ id: "sql-1", type: "sql", title: "SQL", sql: "SELECT * FROM users", selectedDb: "appdb" }],
        activeInnerTabId: "sql-1",
        error: null,
      },
    },
  });
}

async function renderExecuted() {
  const user = userEvent.setup();
  vi.mocked(ExecuteSQLPaged).mockResolvedValue(
    JSON.stringify({
      columns: ["id", "name"],
      rows: [
        { id: 1, name: "ada" },
        { id: 2, name: "bob" },
      ],
      total_count: 2,
    })
  );
  render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
  await user.click(screen.getByText("query.execute"));
  await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());
  return user;
}

/** The SQL editor is a CodeEditor too — the result surfaces are the read-only ones. */
const resultEditor = () =>
  document.querySelector('[data-testid="code-editor"][data-readonly="true"]') as HTMLElement | null;

describe("SqlEditorTab result views", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQLPaged).mockReset();
    provideDrivers(BUILTIN_DRIVERS);
    setupStores();
  });

  it("keeps a picked cell selected so the row detail panel can follow it", async () => {
    const user = await renderExecuted();
    await user.click(screen.getByTitle("query.rowDetail"));

    fireEvent.click(document.querySelector('[data-cell-key="1:name"]') as HTMLElement);

    await waitFor(() => expect(JSON.parse(resultEditor()!.textContent!)).toEqual({ id: 2, name: "bob" }));
  });

  it("renders the result page as JSON without re-running the query", async () => {
    const user = await renderExecuted();
    const callsBefore = vi.mocked(ExecuteSQLPaged).mock.calls.length;

    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));

    expect(JSON.parse(resultEditor()!.textContent!)).toEqual([
      { id: 1, name: "ada" },
      { id: 2, name: "bob" },
    ]);
    expect(vi.mocked(ExecuteSQLPaged).mock.calls.length).toBe(callsBefore);
  });

  it("finds within the result page and closes the find bar when switching to JSON", async () => {
    const user = await renderExecuted();
    await user.click(screen.getByTitle("query.rowDetail"));
    const grid = document.querySelector(".query-table-scroll") as HTMLElement;

    fireEvent.keyDown(grid, { key: "f", code: "KeyF", metaKey: isMac, ctrlKey: !isMac });
    fireEvent.change(screen.getByPlaceholderText("query.find.placeholder"), { target: { value: "bob" } });

    await waitFor(() => expect(JSON.parse(resultEditor()!.textContent!)).toEqual({ id: 2, name: "bob" }));
    expect(screen.getByText("1 / 1")).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));
    expect(screen.queryByPlaceholderText("query.find.placeholder")).toBeNull();
  });

  it("keeps the result grid and its pager in place while the query re-runs or pages", async () => {
    const user = await renderExecuted();
    const grid = document.querySelector(".query-table-scroll");
    let finish!: (raw: string) => void;
    vi.mocked(ExecuteSQLPaged).mockReturnValue(new Promise<string>((resolve) => (finish = resolve)));

    await user.click(screen.getByText("query.execute"));

    expect(document.querySelector(".query-table-scroll")).toBe(grid);
    expect(document.querySelector('[data-cell-key="1:name"]')?.textContent).toBe("bob");
    expect(screen.getByRole("progressbar")).toBeInTheDocument();
    expect(screen.getByTitle("query.nextPage")).toBeDisabled();

    finish(JSON.stringify({ columns: ["id", "name"], rows: [{ id: 3, name: "cy" }], total_count: 1 }));
    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')?.textContent).toBe("cy"));
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(document.querySelector(".query-table-scroll")).toBe(grid);
  });

  it("shows an execution failure card whose retry re-runs the last SQL and page, and whose copy takes the raw error", async () => {
    const user = await renderExecuted();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.mocked(ExecuteSQLPaged).mockRejectedValueOnce("syntax error near 'FORM'");

    await user.click(screen.getByText("query.execute"));

    await screen.findByText("syntax error near 'FORM'");
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText("query.executeFailed")).toBeInTheDocument();
    expect(screen.getByText("syntax error near 'FORM'").className).toContain("select-text");
    expect(screen.queryByRole("button", { name: "query.editFilter" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "query.copyError" }));
    expect(writeText).toHaveBeenCalledWith("syntax error near 'FORM'");

    vi.mocked(ExecuteSQLPaged).mockRejectedValueOnce("second failure");
    await user.click(screen.getByRole("button", { name: "query.retry" }));
    await screen.findByText("second failure");
    expect(screen.queryByText("syntax error near 'FORM'")).toBeNull();
    const calls = vi.mocked(ExecuteSQLPaged).mock.calls;
    expect(calls.at(-1)).toEqual(calls.at(-2));

    vi.mocked(ExecuteSQLPaged).mockResolvedValueOnce(
      JSON.stringify({ columns: ["id", "name"], rows: [{ id: 9, name: "zed" }], total_count: 1 })
    );
    await user.click(screen.getByRole("button", { name: "query.retry" }));
    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')?.textContent).toBe("zed"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows 加载失败 in the status bar when a query fails right after a DML statement", async () => {
    const user = userEvent.setup();
    vi.mocked(ExecuteSQLPaged).mockResolvedValueOnce(JSON.stringify({ affected_rows: 3 }));
    render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
    await user.click(screen.getByText("query.execute"));
    await screen.findByText(/query\.affectedRows/);
    vi.mocked(ExecuteSQLPaged).mockRejectedValueOnce("table users is locked");

    await user.click(screen.getByText("query.execute"));

    await screen.findByText("table users is locked");
    expect(screen.getByText("query.loadFailed")).toBeInTheDocument();
  });

  it("keeps a resized column width when a retry after a failure returns the same columns", async () => {
    const user = await renderExecuted();
    const header = () => document.querySelector('[data-column-header-key="name"]') as HTMLElement;
    Object.defineProperty(window, "prompt", { configurable: true, value: vi.fn().mockReturnValue("240") });
    fireEvent.contextMenu(header(), { clientX: 40, clientY: 20 });
    fireEvent.click(screen.getByText("query.setColumnWidth"));
    expect(header().style.width).toBe("240px");
    vi.mocked(ExecuteSQLPaged).mockRejectedValueOnce("connection reset");

    await user.click(screen.getByText("query.execute"));
    await screen.findByText("connection reset");
    await user.click(screen.getByRole("button", { name: "query.retry" }));

    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());
    expect(header().style.width).toBe("240px");
  });

  it("shows the failure card in the JSON view too", async () => {
    const user = await renderExecuted();
    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));
    vi.mocked(ExecuteSQLPaged).mockRejectedValueOnce("json failure");

    await user.click(screen.getByText("query.execute"));

    await screen.findByText("json failure");
    expect(screen.getByText("query.executeFailed")).toBeInTheDocument();
  });
  describe("shared bottom status bar", () => {
    const pageOf = (n: number, total: number | undefined) =>
      JSON.stringify({
        columns: ["id", "name"],
        rows: Array.from({ length: n }, (_, i) => ({ id: i, name: `n${i}` })),
        ...(total === undefined ? {} : { total_count: total }),
      });

    async function executeWith(raw: string) {
      const user = userEvent.setup();
      vi.mocked(ExecuteSQLPaged).mockResolvedValue(raw);
      render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
      await user.click(screen.getByText("query.execute"));
      await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());
      return user;
    }
    const lastArgs = () => vi.mocked(ExecuteSQLPaged).mock.calls.at(-1)!;

    it("shows the row count and elapsed time in the centre and drops the old top pager", async () => {
      await executeWith(pageOf(3, 3));

      expect(screen.getByText(/query\.rows/)).toHaveTextContent("query.rows · query.elapsedMs");
      expect(screen.queryByText("query.totalRows")).toBeNull();
      expect(screen.getAllByTitle("query.firstPage")).toHaveLength(1);
      expect(screen.getByTitle("query.refreshTable")).toBeInTheDocument();
    });

    it("offers a last-page button only when the result has a total", async () => {
      await executeWith(pageOf(100, 250));
      expect(screen.getByTitle("query.lastPage")).toBeInTheDocument();
    });

    it("has no last-page button without a total", async () => {
      await executeWith(pageOf(100, undefined));
      expect(screen.queryByTitle("query.lastPage")).toBeNull();
      expect(screen.getByTitle("query.nextPage")).toBeEnabled();
    });

    it("refetches page 1 from page 2 via first page, previous page and typing 1", async () => {
      const user = await executeWith(pageOf(100, 300));

      for (const go of [
        () => user.click(screen.getByTitle("query.firstPage")),
        () => user.click(screen.getByTitle("query.prevPage")),
        async () => {
          const input = screen.getByLabelText("query.pageNumber");
          await user.clear(input);
          await user.type(input, "1{Enter}");
        },
      ]) {
        await user.click(screen.getByTitle("query.nextPage"));
        await waitFor(() => expect(lastArgs()[3]).toBe(1));
        await waitFor(() => expect(screen.getByTitle("query.nextPage")).toBeEnabled());
        const before = vi.mocked(ExecuteSQLPaged).mock.calls.length;
        await go();
        await waitFor(() => expect(vi.mocked(ExecuteSQLPaged).mock.calls.length).toBe(before + 1));
        expect(lastArgs()[3]).toBe(0);
        await waitFor(() => expect(screen.getByTitle("query.nextPage")).toBeEnabled());
      }
    });

    it("changes the page size from the settings popover within the allowed range and restarts at page 1", async () => {
      const user = await executeWith(pageOf(100, 1000));
      await user.click(screen.getByTitle("query.tableFooterSettings"));
      const input = await screen.findByLabelText("query.pageSize");

      await user.clear(input);
      await user.type(input, "20000{Enter}");
      await waitFor(() => expect(lastArgs()[4]).toBe(500));
      expect(lastArgs()[3]).toBe(0);

      await user.clear(input);
      await user.type(input, "10{Enter}");
      await waitFor(() => expect(lastArgs()[4]).toBe(50));
    });
  });
});
