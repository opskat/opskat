import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import App from "../App";
import { useTerminalStore } from "../stores/terminalStore";
import { profiled } from "./renderCount";

// Children are stubs without store subscriptions, so a Profiler commit means App itself re-rendered.
const { stub } = vi.hoisted(() => ({ stub: (name: string) => () => <div data-testid={name} /> }));
vi.mock("@/components/layout/Sidebar", () => ({ Sidebar: stub("sidebar") }));
vi.mock("@/components/layout/AssetTree", () => ({ AssetTree: stub("asset-tree") }));
vi.mock("@/components/layout/MainPanel", () => ({ MainPanel: stub("main-panel") }));
vi.mock("@/components/ai/SideAssistantPanel", () => ({ SideAssistantPanel: stub("side-ai") }));
vi.mock("@/components/layout/WindowControls", () => ({ WindowControls: stub("window-controls") }));
vi.mock("@/components/layout/TopBar", () => ({ TopBar: stub("top-bar") }));
vi.mock("@/components/layout/LeftPanel", () => ({ LeftPanel: stub("left-panel") }));
vi.mock("@/components/layout/SideTabList", () => ({ SideTabList: stub("side-tab-list") }));
vi.mock("@/components/command/CommandPaletteDialog", () => ({ CommandPaletteDialog: stub("palette") }));
vi.mock("@/components/snippet/SnippetAssetDrawer", () => ({ SnippetAssetDrawer: stub("snippet") }));
vi.mock("@/components/ai/PermissionDialog", () => ({ PermissionDialog: stub("perm") }));
vi.mock("@/components/approval/OpsctlApprovalDialog", () => ({ OpsctlApprovalDialog: stub("appr") }));
vi.mock("@/components/approval/OpsctlMFADialog", () => ({ OpsctlMFADialog: stub("mfa") }));
vi.mock("@/components/settings/CommandReviewConfigErrorListener", () => ({
  CommandReviewConfigErrorListener: stub("cr"),
}));
vi.mock("@/components/ActiveTasksQuitDialog", () => ({ ActiveTasksQuitDialog: stub("quit") }));
vi.mock("@/extension", async (orig) => ({
  ...(await orig<object>()),
  ExtensionInstallConfirmDialog: stub("ext-install"),
}));
vi.mock("@/components/ErrorBoundary", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("@/extension/init", () => ({ bootstrapExtensions: vi.fn(async () => {}) }));
vi.mock("../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => () => {}),
  EventsEmit: vi.fn(),
  WindowToggleMaximise: vi.fn(),
}));

describe("App render isolation", () => {
  it("does not re-render when terminal sync state changes", async () => {
    const { tree, counter } = profiled(<App />);
    await act(async () => {
      render(tree);
    });
    const before = counter.commits;
    await act(async () => {
      useTerminalStore.setState({
        sessionSync: { s1: { enabled: true, cwd: "/tmp" } as never },
      });
    });
    expect(counter.commits).toBe(before);
  });
});
