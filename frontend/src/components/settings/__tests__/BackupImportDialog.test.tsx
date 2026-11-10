import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
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

  it("keeps the selected custom theme when the backup carries a newer copy of it", async () => {
    const mine = { ...useTerminalThemeStore.getState().getActiveTheme(), id: "custom-mine", name: "Mine" };
    useTerminalThemeStore.setState({ customThemes: [mine], selectedThemeId: "custom-mine" });
    const summary = new backup_svc.BackupSummary({
      version: "1.0",
      exported_at: "2026-09-29T00:00:00Z",
      asset_count: 0,
      group_count: 0,
      has_custom_themes: true,
    });
    const source = fileSource(summary);
    vi.mocked(source.execute).mockResolvedValue(
      new backup_svc.ImportResult({ custom_themes: JSON.stringify([{ ...mine, name: "Mine v2" }]) })
    );
    render(<BackupImportDialog open onOpenChange={vi.fn()} source={source} />);

    fireEvent.click(screen.getByRole("switch", { name: "backup.importThemes" }));
    fireEvent.click(screen.getByRole("button", { name: "backup.import" }));

    await waitFor(() => expect(useTerminalThemeStore.getState().customThemes[0].name).toBe("Mine v2"));
    expect(useTerminalThemeStore.getState().customThemes).toHaveLength(1);
    expect(useTerminalThemeStore.getState().selectedThemeId).toBe("custom-mine");
  });
});
