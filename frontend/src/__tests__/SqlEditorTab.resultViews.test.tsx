import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SqlEditorTab } from "@/components/query/SqlEditorTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQLPaged } from "../../wailsjs/go/query/Query";
import { isMac } from "@/stores/shortcutStore";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value, language, readOnly }: { value?: string; language?: string; readOnly?: boolean }) => (
    <pre data-testid="code-editor" data-language={language} data-readonly={String(!!readOnly)}>
      {value}
    </pre>
  ),
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

  it("shows the failure card in the JSON view too", async () => {
    const user = await renderExecuted();
    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));
    vi.mocked(ExecuteSQLPaged).mockRejectedValueOnce("json failure");

    await user.click(screen.getByText("query.execute"));

    await screen.findByText("json failure");
    expect(screen.getByText("query.executeFailed")).toBeInTheDocument();
  });
});
