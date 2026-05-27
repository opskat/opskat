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
  whereClause: "",
  orderByClause: "",
  sortColumn: null,
  sortDir: null,
  initialFormat: "csv" as const,
  onFormatChange: vi.fn(),
};

describe("ExportTableDataDialog", () => {
  afterEach(() => {
    vi.mocked(App.SelectTableExportFile).mockReset();
    vi.mocked(App.ExecuteSQL).mockReset();
    vi.mocked(App.OpenDirectory).mockReset();
    delete (window as unknown as { go?: unknown }).go;
  });

  it("opens the exported file or its containing folder after a successful export", async () => {
    const user = userEvent.setup();
    vi.mocked(App.SelectTableExportFile).mockResolvedValue("/tmp/opskat/users.csv");
    vi.mocked(App.ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: baseProps.rows }));
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
    vi.mocked(App.ExecuteSQL)
      .mockResolvedValueOnce(JSON.stringify({ rows: firstChunk }))
      .mockResolvedValueOnce(JSON.stringify({ rows: secondChunk }));
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
    expect(App.ExecuteSQL).toHaveBeenNthCalledWith(1, 1, "SELECT * FROM `appdb`.`users` LIMIT 1000 OFFSET 0", "appdb");
    expect(App.ExecuteSQL).toHaveBeenNthCalledWith(
      2,
      1,
      "SELECT * FROM `appdb`.`users` LIMIT 1000 OFFSET 1000",
      "appdb"
    );
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
});
