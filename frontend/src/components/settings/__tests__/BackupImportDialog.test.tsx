import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { BackupImportDialog, type BackupImportSource } from "@/components/settings/BackupImportDialog";
import { backup_svc } from "../../../../wailsjs/go/models";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function fileSource(summary: backup_svc.BackupSummary): BackupImportSource {
  return {
    title: "backup.importTitle",
    passwordPrompt: "backup.encryptedFile",
    successMessage: "backup.importSuccess",
    encrypted: false,
    initialSummary: summary,
    preview: vi.fn(),
    execute: vi.fn(),
  };
}

describe("BackupImportDialog", () => {
  it("shows the custom type count when the backup summary includes one", () => {
    const summary = new backup_svc.BackupSummary({
      version: "1.0",
      exported_at: "2026-09-29T00:00:00Z",
      asset_count: 2,
      group_count: 0,
      custom_type_count: 3,
    });

    render(<BackupImportDialog open onOpenChange={vi.fn()} source={fileSource(summary)} />);

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

    render(<BackupImportDialog open onOpenChange={vi.fn()} source={fileSource(summary)} />);

    expect(screen.queryByTestId("backup-summary-custom-types")).not.toBeInTheDocument();
  });
});
