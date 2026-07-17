import { render, screen } from "@testing-library/react";
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
      />
    );

    expect(screen.getByTestId("sftp-path-input")).toHaveClass("h-6", "py-0");
  });
});
