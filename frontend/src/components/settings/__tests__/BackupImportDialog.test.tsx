import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { BackupImportDialog } from "@/components/settings/BackupImportDialog";
import { backup_svc } from "../../../../wailsjs/go/models";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

describe("BackupImportDialog", () => {
  beforeEach(async () => {
    const mod = await import("../../../../wailsjs/go/system/System");
    vi.mocked(mod.PreviewImportFile).mockReset();
    vi.mocked(mod.ExecuteImportFile).mockReset();
  });

  it("shows the custom type count when the backup summary includes one", () => {
    const summary = new backup_svc.BackupSummary({
      version: "1.0",
      exported_at: "2026-09-29T00:00:00Z",
      asset_count: 2,
      group_count: 0,
      custom_type_count: 3,
    });

    render(
      <BackupImportDialog
        open
        filePath="/tmp/x.json"
        encrypted={false}
        onOpenChange={vi.fn()}
        initialSummary={summary}
      />
    );

    expect(screen.getByTestId("backup-summary-custom-types")).toBeInTheDocument();
    expect(screen.getByText(/3 types/)).toBeInTheDocument();
  });

  it("does not show a custom type row when the backup summary has none", () => {
    const summary = new backup_svc.BackupSummary({
      version: "1.0",
      exported_at: "2026-09-29T00:00:00Z",
      asset_count: 2,
      group_count: 0,
      custom_type_count: 0,
    });

    render(
      <BackupImportDialog
        open
        filePath="/tmp/x.json"
        encrypted={false}
        onOpenChange={vi.fn()}
        initialSummary={summary}
      />
    );

    expect(screen.queryByTestId("backup-summary-custom-types")).not.toBeInTheDocument();
  });
});
