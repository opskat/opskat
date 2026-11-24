import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PathToolbar } from "../PathToolbar";

describe("PathToolbar", () => {
  it("removes vertical padding from the compact path input", () => {
    render(
      <PathToolbar
        activeSyncMode={null}
        currentPath="/data_log"
        directoryFollowMode="off"
        onFollowToggle={vi.fn()}
        onGoHome={vi.fn()}
        onGoUp={vi.fn()}
        onPathInputChange={vi.fn()}
        onPathSubmit={vi.fn()}
        onRefresh={vi.fn()}
        onSyncPanelFromTerminal={vi.fn()}
        onSyncTerminalToPath={vi.fn()}
        paneConnected
        pathInput="/data_log"
        folderTreeVisible
        onToggleFolderTree={vi.fn()}
      />
    );

    expect(screen.getByTestId("sftp-path-input")).toHaveClass("h-6", "py-0");
  });

  it.each([true, false])("exposes the folder tree toggle as a named button pressed=%s", (visible) => {
    const onToggleFolderTree = vi.fn();
    render(
      <PathToolbar
        activeSyncMode={null}
        currentPath="/srv"
        directoryFollowMode="off"
        onFollowToggle={vi.fn()}
        onGoHome={vi.fn()}
        onGoUp={vi.fn()}
        onPathInputChange={vi.fn()}
        onPathSubmit={vi.fn()}
        onRefresh={vi.fn()}
        onSyncPanelFromTerminal={vi.fn()}
        onSyncTerminalToPath={vi.fn()}
        paneConnected
        pathInput="/srv"
        folderTreeVisible={visible}
        onToggleFolderTree={onToggleFolderTree}
      />
    );

    const toggle = screen.getByRole("button", { name: "sftp.folderTree.toggle" });
    expect(toggle).toHaveAttribute("aria-pressed", String(visible));
    fireEvent.click(toggle);
    expect(onToggleFolderTree).toHaveBeenCalledTimes(1);
  });
});
