import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { AlterTableDialog } from "@/components/query/AlterTableDialog";
import { ExecuteSQL } from "../../wailsjs/go/query/Query";

describe("AlterTableDialog MSSQL column loading", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
  });

  it("loads columns from INFORMATION_SCHEMA for MSSQL instead of SHOW FULL COLUMNS", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ rows: [] }));

    render(
      <AlterTableDialog
        open
        onOpenChange={() => {}}
        assetId={1}
        database="appdb"
        table="dbo.users"
        driver="mssql"
        onSuccess={() => {}}
      />
    );

    await waitFor(() => {
      expect(vi.mocked(ExecuteSQL).mock.calls.length).toBeGreaterThan(0);
    });

    const sql = String(vi.mocked(ExecuteSQL).mock.calls[0][1]);
    expect(sql).toContain("INFORMATION_SCHEMA.COLUMNS");
    expect(sql).toContain("TABLE_SCHEMA = 'dbo'");
    expect(sql).toContain("TABLE_NAME = 'users'");
    expect(sql).not.toContain("SHOW FULL COLUMNS");
  });
});
