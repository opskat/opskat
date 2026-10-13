import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ExportTableDataDialog } from "@/components/query/ExportTableDataDialog";
import * as QueryBinder from "../../wailsjs/go/query/Query";
import * as SystemBinder from "../../wailsjs/go/system/System";

// 兼容旧测试代码中的 App.* 引用：聚合到一个对象。
const App = {
  ...QueryBinder,
  ...SystemBinder,
};

const baseProps = {
  open: true,
  onOpenChange: vi.fn(),
  assetId: 1,
  database: "appdb",
  table: "users",
  driver: "mysql",
  columns: ["id", "name"],
  rows: [{ id: 1, name: "Alice" }],
  totalRows: 1,
  page: 0,
  pageSize: 50,
  filters: [] as never[],
  sorts: [] as { column: string; dir: "asc" | "desc" }[],
  initialFormat: "csv" as const,
  onFormatChange: vi.fn(),
};

function mockCursor(chunks: { rows: Record<string, unknown>[]; done: boolean }[]) {
  vi.mocked(App.OpenTableExport).mockResolvedValue({ id: "cursor-1", columns: ["id", "name"] });
  vi.mocked(App.CloseTableExport).mockResolvedValue(undefined);
  const read = vi.mocked(App.ReadTableExport);
  for (const chunk of chunks) read.mockResolvedValueOnce(chunk);
}

describe("ExportTableDataDialog", () => {
  afterEach(() => {
    vi.mocked(App.SelectTableExportFile).mockReset();
    vi.mocked(App.OpenTableExport).mockReset();
    vi.mocked(App.ReadTableExport).mockReset();
    vi.mocked(App.CloseTableExport).mockReset();
    vi.mocked(App.QueryTableRows).mockReset();
    vi.mocked(App.ExportTableToXlsx).mockReset();
    vi.mocked(App.ExecuteSQL).mockReset();
    vi.mocked(App.OpenDirectory).mockReset();
    delete (window as unknown as { go?: unknown }).go;
  });

  it("opens the exported file or its containing folder after a successful export", async () => {
    const user = userEvent.setup();
    vi.mocked(App.SelectTableExportFile).mockResolvedValue("/tmp/opskat/users.csv");
    mockCursor([{ rows: baseProps.rows, done: true }]);
    const writeFile = vi.fn().mockResolvedValue(undefined);
    Object.assign(window, {
      go: {
        app: {
          App: {
            WriteTableExportFile: writeFile,
          },
        },
      },
    });

    render(<ExportTableDataDialog {...baseProps} />);

    await user.click(screen.getByRole("button", { name: /query.exportChooseFile/ }));
    await screen.findByText("/tmp/opskat/users.csv");
    await user.click(screen.getByRole("button", { name: /query.exportStart/ }));

    await waitFor(() => expect(writeFile).toHaveBeenCalled());
    await user.click(await screen.findByRole("button", { name: /query.openExport/ }));
    await user.click(await screen.findByText("query.openExportFile"));
    expect(App.OpenDirectory).toHaveBeenCalledWith("/tmp/opskat/users.csv");

    await user.click(screen.getByRole("button", { name: /query.openExport/ }));
    await user.click(await screen.findByText("query.openExportFolder"));
    expect(App.OpenDirectory).toHaveBeenCalledWith("/tmp/opskat");
  });

  it("exports all data in bounded chunks instead of one full-table payload", async () => {
    const user = userEvent.setup();
    const firstChunk = Array.from({ length: 1000 }, (_, index) => ({ id: index + 1, name: `User ${index + 1}` }));
    const secondChunk = [{ id: 1001, name: "Last User" }];
    vi.mocked(App.SelectTableExportFile).mockResolvedValue("/tmp/opskat/users.csv");
    mockCursor([
      { rows: firstChunk, done: false },
      { rows: secondChunk, done: true },
    ]);
    const writeFile = vi.fn().mockResolvedValue(undefined);
    Object.assign(window, {
      go: {
        app: {
          App: {
            WriteTableExportFile: writeFile,
          },
        },
      },
    });

    render(<ExportTableDataDialog {...baseProps} totalRows={1001} />);

    await user.click(screen.getByRole("button", { name: /query.exportChooseFile/ }));
    await screen.findByText("/tmp/opskat/users.csv");
    await user.click(screen.getByRole("button", { name: /query.exportStart/ }));

    await waitFor(() => expect(writeFile).toHaveBeenCalledTimes(2));
    // 整表只打开一个游标(一次取数),再按批读出;不再按页码发多次查询。
    expect(App.OpenTableExport).toHaveBeenCalledTimes(1);
    expect(App.OpenTableExport).toHaveBeenCalledWith(1, "appdb", "users", { filters: [], sorts: [] });
    expect(App.ReadTableExport).toHaveBeenNthCalledWith(1, "cursor-1", 1000);
    expect(App.ReadTableExport).toHaveBeenNthCalledWith(2, "cursor-1", 1000);
    expect(App.CloseTableExport).toHaveBeenCalledWith("cursor-1");
    expect(App.QueryTableRows).not.toHaveBeenCalled();
    expect(App.ExecuteSQL).not.toHaveBeenCalled();
    expect(writeFile).toHaveBeenNthCalledWith(
      1,
      "/tmp/opskat/users.csv",
      expect.stringContaining("id,name\n1,User 1"),
      { encoding: "utf-8", append: false }
    );
    expect(writeFile).toHaveBeenNthCalledWith(2, "/tmp/opskat/users.csv", "\n1001,Last User", {
      encoding: "utf-8",
      append: true,
    });
  });

  const filters = [
    { kind: "condition", join: "and", disabled: false, column: "name", operator: "contains", value: "a" },
  ] as never[];
  const sorts = [{ column: "id", dir: "desc" as const }];

  async function startExport(user: ReturnType<typeof userEvent.setup>, path: string) {
    await user.click(screen.getByRole("button", { name: /query.exportChooseFile/ }));
    await screen.findByText(path);
    await user.click(screen.getByRole("button", { name: /query.exportStart/ }));
  }

  it.each(["csv", "tsv"] as const)(
    "fetches %s data with the structured filter and sort, never SQL text",
    async (format) => {
      const user = userEvent.setup();
      vi.mocked(App.SelectTableExportFile).mockResolvedValue(`/tmp/opskat/users.${format}`);
      mockCursor([{ rows: baseProps.rows, done: true }]);
      Object.assign(window, { go: { app: { App: { WriteTableExportFile: vi.fn().mockResolvedValue(undefined) } } } });

      render(<ExportTableDataDialog {...baseProps} initialFormat={format} filters={filters} sorts={sorts} />);
      await startExport(user, `/tmp/opskat/users.${format}`);

      await waitFor(() => expect(App.CloseTableExport).toHaveBeenCalled());
      expect(App.OpenTableExport).toHaveBeenCalledWith(1, "appdb", "users", { filters, sorts });
      expect(App.QueryTableRows).not.toHaveBeenCalled();
      expect(App.ExecuteSQL).not.toHaveBeenCalled();
    }
  );

  it.each([
    ["all", { scope: "all" }],
    ["page", { scope: "page" }],
  ] as const)("hands the %s-scope XLSX export to the backend as a structured request", async (_name, expected) => {
    const user = userEvent.setup();
    vi.mocked(App.SelectTableExportFile).mockResolvedValue("/tmp/opskat/users.xlsx");
    vi.mocked(App.ExportTableToXlsx).mockResolvedValue(undefined);

    render(
      <ExportTableDataDialog
        {...baseProps}
        initialFormat="xlsx"
        filters={filters}
        sorts={sorts}
        page={2}
        pageSize={50}
      />
    );
    if (expected.scope === "page") {
      const scopeSelect = screen.getAllByRole("combobox").find((el) => el.textContent?.includes("query.exportAllData"));
      expect(scopeSelect).toBeDefined();
      await user.click(scopeSelect!);
      await user.click(await screen.findByRole("option", { name: "query.exportPageData" }));
    }
    await startExport(user, "/tmp/opskat/users.xlsx");

    await waitFor(() => expect(App.ExportTableToXlsx).toHaveBeenCalled());
    const [assetId, database, table, request, filePath] = vi.mocked(App.ExportTableToXlsx).mock.calls[0];
    expect([assetId, database, table, filePath]).toEqual([1, "appdb", "users", "/tmp/opskat/users.xlsx"]);
    expect(request).toEqual({
      filters,
      sorts,
      columns: ["id", "name"],
      scope: expected.scope,
      page: 2,
      pageSize: 50,
    });
    expect(App.ExecuteSQL).not.toHaveBeenCalled();
  });
});
