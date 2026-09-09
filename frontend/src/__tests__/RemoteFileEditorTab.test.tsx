import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MainPanel } from "@/components/layout/MainPanel";
import { RemoteFileEditorTab } from "@/components/terminal/editor/RemoteFileEditorTab";
import { useAssetStore } from "@/stores/assetStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { openRemoteFileEditorTab, useTabStore, type EditorTabMeta } from "@/stores/tabStore";

const { readSessionTextMock } = vi.hoisted(() => ({ readSessionTextMock: vi.fn() }));

vi.mock("@/lib/externalEditApi", async () => {
  const actual = await vi.importActual<typeof import("@/lib/externalEditApi")>("@/lib/externalEditApi");
  return { ...actual, readExternalEditSessionText: readSessionTextMock };
});

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value, testId }: { value?: string; testId?: string }) => <pre data-testid={testId}>{value}</pre>,
}));

vi.mock("@/components/terminal/FileManagerPanel", () => ({
  FileManagerPanel: () => <div data-testid="file-manager-panel" />,
}));

function editorMeta(overrides: Partial<EditorTabMeta> = {}): EditorTabMeta {
  return {
    type: "editor",
    sessionId: "sess-1",
    assetId: 7,
    assetName: "prod-web-1",
    remotePath: "/etc/nginx/nginx.conf",
    ...overrides,
  };
}

describe("RemoteFileEditorTab", () => {
  beforeEach(() => {
    readSessionTextMock.mockReset();
    readSessionTextMock.mockResolvedValue("server {\n  listen 80;\n}\n");
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
  });

  it("shows the asset, the full remote path and the decoded session text", async () => {
    render(<RemoteFileEditorTab meta={editorMeta()} />);

    expect(await screen.findByTestId("remote-file-editor-content")).toHaveTextContent("listen 80;");
    expect(readSessionTextMock).toHaveBeenCalledWith("sess-1");
    expect(screen.getByText("prod-web-1")).toBeInTheDocument();
    expect(screen.getByText("/etc/nginx/nginx.conf")).toBeInTheDocument();
  });

  it("surfaces a read failure instead of an empty editor", async () => {
    readSessionTextMock.mockRejectedValueOnce(new Error("boom"));

    render(<RemoteFileEditorTab meta={editorMeta()} />);

    expect(await screen.findByText("externalEdit.builtIn.loadFailed")).toBeInTheDocument();
    expect(screen.queryByTestId("remote-file-editor-content")).not.toBeInTheDocument();
  });

  it("opens one tab per asset and remote path and never creates a second session", async () => {
    const createSession = vi.fn().mockResolvedValue({ sessionId: "sess-1", assetName: "prod-web-1" });

    const firstId = await openRemoteFileEditorTab({
      assetId: 7,
      remotePath: "/etc/nginx/nginx.conf",
      createSession,
    });
    useTabStore.setState({ activeTabId: null });
    const secondId = await openRemoteFileEditorTab({
      assetId: 7,
      remotePath: "/etc/nginx/nginx.conf",
      createSession,
    });

    expect(secondId).toBe(firstId);
    expect(createSession).toHaveBeenCalledTimes(1);
    const { tabs, activeTabId } = useTabStore.getState();
    expect(tabs).toHaveLength(1);
    expect(activeTabId).toBe(firstId);
    expect(tabs[0].label).toBe("nginx.conf");
    expect(tabs[0].meta).toEqual(editorMeta());
  });

  it("opens a separate tab for the same path on another asset", async () => {
    await openRemoteFileEditorTab({
      assetId: 7,
      remotePath: "/etc/nginx/nginx.conf",
      createSession: async () => ({ sessionId: "sess-1", assetName: "prod-web-1" }),
    });
    await openRemoteFileEditorTab({
      assetId: 8,
      remotePath: "/etc/nginx/nginx.conf",
      createSession: async () => ({ sessionId: "sess-2", assetName: "prod-web-2" }),
    });

    expect(useTabStore.getState().tabs).toHaveLength(2);
  });

  it("persists the editor tab so it comes back after a restart", async () => {
    await openRemoteFileEditorTab({
      assetId: 7,
      remotePath: "/etc/nginx/nginx.conf",
      createSession: async () => ({ sessionId: "sess-1", assetName: "prod-web-1" }),
    });

    expect(JSON.parse(localStorage.getItem("tab_store") ?? "{}").tabs).toEqual([
      expect.objectContaining({ type: "editor", label: "nginx.conf", meta: editorMeta() }),
    ]);

    vi.resetModules();
    const restored = await import("@/stores/tabStore");

    expect(restored.useTabStore.getState().tabs).toEqual([
      expect.objectContaining({ type: "editor", label: "nginx.conf", meta: editorMeta() }),
    ]);
  });

  it("renders the restored editor tab full width without a file panel", async () => {
    useLayoutStore.setState({ tabBarLayout: "left" });
    useAssetStore.setState({ assets: [], initialized: true });
    useTabStore.setState({
      tabs: [{ id: "editor-sess-1", type: "editor", label: "nginx.conf", meta: editorMeta() }],
      activeTabId: "editor-sess-1",
    });

    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);

    expect(await screen.findByTestId("remote-file-editor-content")).toBeVisible();
    await waitFor(() => expect(screen.getByText("/etc/nginx/nginx.conf")).toBeVisible());
    expect(screen.queryByTestId("file-manager-panel")).not.toBeInTheDocument();
  });
});
