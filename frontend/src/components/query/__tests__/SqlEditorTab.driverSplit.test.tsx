import { describe, expect, it, beforeEach, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { SqlEditorTab } from "@/components/query/SqlEditorTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ClassifySQLStatements, ExecuteSQLPaged } from "../../../../wailsjs/go/query/Query";
import { fakeDriverWith, provideDrivers } from "@/__tests__/dbDriverFixtures";
import { mounted } from "./fakeSqlEditor";

vi.mock("@/components/CodeEditor", async () => ({
  CodeEditor: (await import("./FakeCodeEditor")).FakeCodeEditor,
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), info: vi.fn(), success: vi.fn(), warning: vi.fn() } }));

const SPLITTING = fakeDriverWith(["classify_statements"], "oradb");
const PLAIN = fakeDriverWith([], "plaindb");

const QUERY = "SELECT '中文😀' FROM dual";
const BLOCK = "BEGIN\n  DELETE FROM t;\n  NULL;\nEND;";
const SCRIPT = `${QUERY};\n${BLOCK}`;

/** 驱动的切分：parts 里出现在输入中的每一段是一条语句，按位置排列，偏移按 Go 的 UTF-8 字节给出。 */
function driverSplits(...parts: Array<[text: string, type: string, reason?: string]>) {
  const bytes = (s: string) => new TextEncoder().encode(s).length;
  vi.mocked(ClassifySQLStatements).mockImplementation(async (_asset, sql) =>
    parts
      .filter(([text]) => sql.includes(text))
      .map(([text, type, reason]) => {
        const start = bytes(sql.slice(0, sql.indexOf(text)));
        return { start, end: start + bytes(text), text, type, dangerous: reason !== undefined, reason };
      })
      .sort((a, b) => a.start - b.start)
  );
}

function setup(sql: string, driver = SPLITTING.id) {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 1, assetName: "db", assetIcon: "", assetType: "database", driver },
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
        innerTabs: [{ id: "sql-1", type: "sql", title: "SQL", sql, selectedDb: "appdb" }],
        activeInnerTabId: "sql-1",
        error: null,
      },
    },
  });
  render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
  return mounted.current!;
}

/** 编辑器挂载后先按已有文本取一次切分；等它到达。 */
async function setupSplit(sql: string) {
  const ed = setup(sql);
  await waitFor(() => expect(ClassifySQLStatements).toHaveBeenCalledWith(1, sql));
  await act(async () => {});
  return ed;
}

const executed = () => vi.mocked(ExecuteSQLPaged).mock.calls.map((c) => c[1]);
const splitInputs = () => vi.mocked(ClassifySQLStatements).mock.calls.map((c) => c[1]);
const pause = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe("SqlEditorTab with a driver that splits and classifies statements", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQLPaged).mockReset();
    vi.mocked(ExecuteSQLPaged).mockResolvedValue(
      JSON.stringify({ columns: ["id"], rows: [{ id: 1 }], total_count: 1 })
    );
    vi.mocked(ClassifySQLStatements).mockReset();
    vi.mocked(toast.error).mockReset();
    provideDrivers([SPLITTING, PLAIN]);
    driverSplits([QUERY, "SELECT"], [BLOCK, "PLSQL BLOCK"]);
  });

  it("asks the driver for the split once the user pauses typing, not on every keystroke", async () => {
    const ed = await setupSplit("");
    vi.mocked(ClassifySQLStatements).mockClear();

    act(() => ed.editor.setValue("S"));
    act(() => ed.editor.setValue("SE"));
    act(() => ed.editor.setValue(SCRIPT));
    await waitFor(() => expect(ClassifySQLStatements).toHaveBeenCalled());
    await act(() => pause(400));
    expect(splitInputs()).toEqual([SCRIPT]);
  });

  it("highlights the whole PL/SQL block the cursor is in", async () => {
    const ed = await setupSplit(SCRIPT);
    act(() => ed.setFocus(true));
    act(() => ed.setCursor(SCRIPT.indexOf("NULL")));

    expect(ed.decorations()).toHaveLength(1);
    expect(ed.decorations()[0].range).toMatchObject({ startLineNumber: 2, endLineNumber: 5 });
  });

  it("runs the driver's statement under the cursor, a PL/SQL block as a whole", async () => {
    const ed = await setupSplit(SCRIPT);
    act(() => ed.setCursor(SCRIPT.indexOf("NULL")));
    act(() => ed.runMod());

    await waitFor(() => expect(executed()).toEqual([BLOCK]));
    // the block contains a DELETE, but the driver says the statement is a PL/SQL block
    expect(screen.queryByText("query.dangerousSqlTitle")).toBeNull();
  });

  it("splits again at execute time, using the text at that moment", async () => {
    const ed = await setupSplit(SCRIPT);
    const edited = `${BLOCK}\n${QUERY}`;
    act(() => ed.editor.setValue(edited));
    act(() => ed.setCursor(edited.length));
    act(() => ed.runMod());

    await waitFor(() => expect(executed()).toEqual([QUERY]));
    expect(splitInputs()).toContain(edited);
  });

  it("keeps the selection and run-all texts as they are", async () => {
    const ed = await setupSplit(SCRIPT);
    act(() => ed.select(0, QUERY.length));
    act(() => ed.runMod());
    await waitFor(() => expect(executed()).toEqual([QUERY]));

    act(() => ed.runModShift());
    await waitFor(() => expect(executed()).toEqual([QUERY, SCRIPT]));
  });

  it.each([
    ["a DELETE", "DELETE FROM t WHERE id = 1", "DELETE", undefined],
    ["a DROP", "DROP TABLE t PURGE", "DROP TABLE", undefined],
    ["an ALTER", "ALTER TABLE t ADD c NUMBER", "ALTER TABLE", undefined],
    ["an UPDATE without WHERE", "UPDATE t SET a = 1", "UPDATE", "no_where_update"],
  ])("asks for confirmation for %s the driver reports, and runs it once confirmed", async (_n, sql, type, reason) => {
    driverSplits([sql, type, reason]);
    const ed = await setupSplit(sql);
    act(() => ed.runModShift());

    expect(await screen.findByText("query.dangerousSqlTitle")).toBeInTheDocument();
    expect(executed()).toEqual([]);
    await userEvent.setup().click(screen.getAllByText("query.execute").at(-1)!);
    await waitFor(() => expect(executed()).toEqual([sql]));
  });

  it.each([
    // the semicolon split would read "DROP TABLE y..." as a second statement here
    ["an Oracle q-quoted string the semicolon split misreads", "SELECT q'{it's; DROP TABLE y}' FROM dual", "SELECT"],
    ["a MERGE", "MERGE INTO t USING s ON (t.id = s.id) WHEN MATCHED THEN UPDATE SET t.a = s.a", "MERGE"],
  ])("runs %s directly when the driver says it is harmless", async (_n, sql, type) => {
    driverSplits([sql, type]);
    const ed = await setupSplit(sql);
    act(() => ed.runModShift());

    await waitFor(() => expect(executed()).toEqual([sql]));
    expect(screen.queryByText("query.dangerousSqlTitle")).toBeNull();
  });

  it.each([
    [
      "cannot split the SQL",
      'The driver couldn\'t split this SQL: driver "oradb": cannot split SQL: unterminated q-quote',
    ],
    ["is unavailable", 'The database driver is unavailable: "oradb" (its extension "oracle" is no longer loaded)'],
  ])("shows the reason and runs nothing when the driver %s", async (_n, reason) => {
    const ed = await setupSplit(SCRIPT);
    vi.mocked(ClassifySQLStatements).mockRejectedValue(reason);

    for (const run of [() => ed.runMod(), () => ed.runModShift()]) {
      vi.mocked(toast.error).mockClear();
      act(run);
      await waitFor(() =>
        expect(toast.error).toHaveBeenCalledWith("query.statementSplitFailed", { description: reason })
      );
    }
    act(() => ed.select(0, 6));
    act(() => ed.runMod());
    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    expect(executed()).toEqual([]);
    expect(screen.queryByText("query.dangerousSqlTitle")).toBeNull();
  });

  it("is runnable only where the driver found a statement", async () => {
    driverSplits();
    // SQL*Plus remark: the semicolon split would offer it as a statement
    await setupSplit("REM nothing to run here");
    await waitFor(() => expect(screen.getByTestId("sql-execute-button")).toBeDisabled());
  });

  it("stays runnable while the driver cannot split, so running shows the driver's reason", async () => {
    const reason = "The driver couldn't split this SQL: unterminated q-quote";
    vi.mocked(ClassifySQLStatements).mockRejectedValue(reason);
    await setupSplit("SELECT q'{oops FROM dual");

    expect(screen.getByTestId("sql-execute-button")).toBeEnabled();
    await userEvent.setup().click(screen.getByTestId("sql-execute-button"));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("query.statementSplitFailed", { description: reason })
    );
    expect(executed()).toEqual([]);
  });

  it("keeps the semicolon split for a driver that does not declare it", async () => {
    const ed = setup("SELECT 1;\nSELECT 2;", PLAIN.id);
    act(() => ed.editor.setValue("SELECT 1;\nDROP TABLE x;"));
    await act(() => pause(400));
    act(() => ed.setCursor(2));
    act(() => ed.runMod());

    await waitFor(() => expect(executed()).toEqual(["SELECT 1"]));
    act(() => ed.runModShift());
    expect(await screen.findByText("query.dangerousSqlTitle")).toBeInTheDocument();
    expect(ClassifySQLStatements).not.toHaveBeenCalled();
  });
});
