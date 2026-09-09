import { StrictMode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type * as MonacoNS from "monaco-editor";
import { TooltipProvider } from "@opskat/ui";
import { MainPanel } from "@/components/layout/MainPanel";
import { SideTabList } from "@/components/layout/SideTabList";
import { TopTabBar } from "@/components/layout/TopTabBar";
import { RemoteFileEditorTab } from "@/components/terminal/editor/RemoteFileEditorTab";
import { useAssetStore } from "@/stores/assetStore";
import { useExternalEditStore } from "@/stores/externalEditStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { openRemoteFileEditorTab, useTabStore, type EditorTabMeta, type Tab } from "@/stores/tabStore";

const {
  readSessionTextMock,
  saveSessionTextMock,
  refreshSessionMock,
  resolveConflictMock,
  compareSessionMock,
  prepareMergeMock,
} = vi.hoisted(() => ({
  readSessionTextMock: vi.fn(),
  saveSessionTextMock: vi.fn(),
  refreshSessionMock: vi.fn(),
  resolveConflictMock: vi.fn(),
  compareSessionMock: vi.fn(),
  prepareMergeMock: vi.fn(),
}));

vi.mock("@/lib/externalEditApi", async () => {
  const actual = await vi.importActual<typeof import("@/lib/externalEditApi")>("@/lib/externalEditApi");
  return {
    ...actual,
    readExternalEditSessionText: readSessionTextMock,
    saveExternalEditSessionText: saveSessionTextMock,
    refreshExternalEditSession: refreshSessionMock,
    resolveExternalEditConflict: resolveConflictMock,
    compareExternalEditSession: compareSessionMock,
    prepareExternalEditMerge: prepareMergeMock,
  };
});

// Monaco 在测试里由这个替身承载：它记录 addCommand 注册的键位与装饰集合，
// 让「⌘S 绑定」「改动行标记」这两条契约可以被观察，而不是靠断言 mock 自己喂进去的值。
const { codeEditorController } = vi.hoisted(() => ({
  codeEditorController: {
    mounts: new Map<string, () => void>(),
    changers: new Map<string, (next: string) => void>(),
    commands: new Map<number, () => void>(),
    decorations: [] as Array<{ range: { startLineNumber: number; endLineNumber: number } }>,
  },
}));

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({
    value,
    testId,
    onChange,
    onMount,
  }: {
    value?: string;
    testId?: string;
    onChange?: (next: string) => void;
    onMount?: (editor: unknown, monaco: unknown) => void;
  }) => {
    const key = testId || "unknown";
    codeEditorController.changers.set(key, (next) => onChange?.(next));
    codeEditorController.mounts.set(key, () => {
      const collection = {
        set: vi.fn((next: Array<{ range: { startLineNumber: number; endLineNumber: number } }>) => {
          codeEditorController.decorations = next;
        }),
        clear: vi.fn(() => {
          codeEditorController.decorations = [];
        }),
      };
      const editor = {
        addCommand: vi.fn((keybinding: number, handler: () => void) => {
          codeEditorController.commands.set(keybinding, handler);
        }),
        createDecorationsCollection: vi.fn(() => collection),
        getTopForLineNumber: vi.fn((lineNumber: number) => (lineNumber - 1) * 19),
        getScrollTop: vi.fn(() => 0),
        onDidScrollChange: vi.fn(() => ({ dispose: vi.fn() })),
        onDidLayoutChange: vi.fn(() => ({ dispose: vi.fn() })),
        onDidContentSizeChange: vi.fn(() => ({ dispose: vi.fn() })),
        revealLineInCenter: vi.fn(),
        setPosition: vi.fn(),
      };
      const monaco = {
        KeyMod: { CtrlCmd: 2048 },
        KeyCode: { KeyS: 49 },
        Range: vi.fn(function Range(startLine: number, startColumn: number, endLine: number, endColumn: number) {
          return { startLineNumber: startLine, startColumn, endLineNumber: endLine, endColumn };
        }),
        editor: { OverviewRulerLane: { Full: 7 } },
      } as unknown as typeof MonacoNS;
      onMount?.(editor, monaco);
    });
    return <pre data-testid={testId}>{value}</pre>;
  },
}));

const CMD_S = 2048 | 49;

function mountEditors() {
  act(() => {
    for (const mount of [...codeEditorController.mounts.values()]) mount();
  });
}

function typeInEditor(next: string) {
  act(() => codeEditorController.changers.get("remote-file-editor-content")?.(next));
}

async function pressSave() {
  await act(async () => {
    codeEditorController.commands.get(CMD_S)?.();
  });
}

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

function makeSession(overrides: Record<string, unknown> = {}) {
  return {
    id: "sess-1",
    assetId: 7,
    assetName: "prod-web-1",
    documentKey: "7:/etc/nginx/nginx.conf",
    sessionId: "ssh-1",
    remotePath: "/etc/nginx/nginx.conf",
    remoteRealPath: "/etc/nginx/nginx.conf",
    localPath: "/tmp/sess-1/nginx.conf",
    workspaceRoot: "/tmp",
    workspaceDir: "/tmp/sess-1",
    editorId: "builtin",
    editorName: "OpsKat",
    editorPath: "",
    originalSha256: "base",
    originalSize: 10,
    originalModTime: 1,
    originalEncoding: "utf-8",
    lastLocalSha256: "base",
    dirty: false,
    state: "clean",
    hidden: false,
    expired: false,
    createdAt: 1,
    updatedAt: 2,
    lastLaunchedAt: 1,
    lastSyncedAt: 1,
    ...overrides,
  };
}

function editorTab(overrides: Partial<EditorTabMeta> = {}): Tab {
  const meta = editorMeta(overrides);
  return { id: `editor-${meta.sessionId}`, type: "editor", label: "nginx.conf", meta };
}

describe("RemoteFileEditorTab", () => {
  beforeEach(() => {
    readSessionTextMock.mockReset();
    readSessionTextMock.mockResolvedValue("server {\n  listen 80;\n}\n");
    saveSessionTextMock.mockReset();
    refreshSessionMock.mockReset();
    resolveConflictMock.mockReset();
    compareSessionMock.mockReset();
    prepareMergeMock.mockReset();
    codeEditorController.mounts.clear();
    codeEditorController.changers.clear();
    codeEditorController.commands.clear();
    codeEditorController.decorations = [];
    localStorage.clear();
    useTabStore.setState({
      tabs: [],
      activeTabId: null,
      unsavedTabIds: [],
      pendingCloseTabId: null,
      restoredTabIds: [],
    });
    useTerminalStore.setState({ tabData: {}, connections: {}, connectingAssetIds: new Set() });
    useExternalEditStore.setState({
      sessions: {},
      savingSessionId: null,
      pendingConflict: null,
      compareResult: null,
      mergeResult: null,
    });
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

  it("shows the editor tab in the top tab bar", async () => {
    useTabStore.setState({
      tabs: [{ id: "editor-sess-1", type: "editor", label: "nginx.conf", meta: editorMeta() }],
      activeTabId: "editor-sess-1",
    });

    render(<TopTabBar />);

    const label = await screen.findByText("nginx.conf");
    expect(label).toBeVisible();
    // 同名文件可能来自不同资产：tab 上必须同时看得出资产与完整远程路径。
    const item = label.closest("[title]") as HTMLElement;
    expect(item.title).toContain("prod-web-1");
    expect(item.title).toContain("/etc/nginx/nginx.conf");
  });

  it("marks an editor tab with unsaved changes in the top tab bar", () => {
    useTabStore.setState({ tabs: [editorTab()], activeTabId: "editor-sess-1" });

    render(<TopTabBar />);
    expect(screen.queryByTestId("tab-unsaved-marker")).not.toBeInTheDocument();

    act(() => useTabStore.getState().setTabUnsaved("editor-sess-1", true));

    expect(screen.getByTestId("tab-unsaved-marker")).toBeInTheDocument();
  });

  it("marks an editor tab with unsaved changes in the side tab list", () => {
    useLayoutStore.setState({ tabBarLayout: "left", leftPanelWidth: 220, filterOpen: false });
    useTabStore.setState({ tabs: [editorTab()], activeTabId: "editor-sess-1" });

    render(
      <TooltipProvider>
        <SideTabList />
      </TooltipProvider>
    );
    expect(screen.queryByTestId("tab-unsaved-marker")).not.toBeInTheDocument();

    act(() => useTabStore.getState().setTabUnsaved("editor-sess-1", true));

    expect(screen.getByTestId("tab-unsaved-marker")).toBeInTheDocument();
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

  // 与 MainPanel 一样从 tabStore 取 meta：冲突重新读取会换绑会话 id，界面必须跟着换。
  function EditorHost() {
    const meta = useTabStore((s) => s.tabs.find((tab) => tab.id === "editor-sess-1")?.meta);
    return meta?.type === "editor" ? <RemoteFileEditorTab meta={meta} /> : null;
  }

  async function renderOpenEditor() {
    useTabStore.setState({ tabs: [editorTab()], activeTabId: "editor-sess-1" });
    render(<EditorHost />);
    await screen.findByTestId("remote-file-editor-content");
    mountEditors();
  }

  it("marks the tab and the document strip unsaved on input without touching the remote", async () => {
    await renderOpenEditor();

    typeInEditor("server {\n  listen 8080;\n}\n");

    expect(await screen.findByTestId("remote-file-editor-unsaved")).toBeInTheDocument();
    expect(useTabStore.getState().unsavedTabIds).toContain("editor-sess-1");
    expect(saveSessionTextMock).not.toHaveBeenCalled();
  });

  it("marks only the changed lines in the editor gutter", async () => {
    await renderOpenEditor();

    typeInEditor("server {\n  listen 8080;\n}\n");

    await waitFor(() => expect(codeEditorController.decorations).toHaveLength(1));
    expect(codeEditorController.decorations[0].range).toMatchObject({ startLineNumber: 2, endLineNumber: 2 });
  });

  it("saves on Cmd/Ctrl+S, showing progress then the write-back time and clearing the unsaved marks", async () => {
    let releaseSave: (result: unknown) => void = () => {};
    saveSessionTextMock.mockImplementation(
      () =>
        new Promise((resolve) => {
          releaseSave = resolve;
        })
    );
    await renderOpenEditor();
    typeInEditor("server {\n  listen 8080;\n}\n");

    await pressSave();

    expect(saveSessionTextMock).toHaveBeenCalledWith("sess-1", "server {\n  listen 8080;\n}\n");
    expect(await screen.findByTestId("remote-file-editor-saving")).toBeInTheDocument();
    expect(screen.getByTestId("remote-file-editor-save")).toBeDisabled();

    await act(async () => {
      releaseSave({ status: "saved", message: "远程文件已保存", session: makeSession() });
    });

    expect(await screen.findByTestId("remote-file-editor-saved-at")).toBeInTheDocument();
    expect(screen.queryByTestId("remote-file-editor-unsaved")).not.toBeInTheDocument();
    expect(useTabStore.getState().unsavedTabIds).not.toContain("editor-sess-1");
  });

  it("stops on a remote-drift conflict and offers merge, diff, reread and a confirmed overwrite", async () => {
    saveSessionTextMock.mockResolvedValue({
      status: "conflict_remote_changed",
      message: "远程文件已有新版本",
      session: makeSession({ state: "conflict" }),
      conflict: { documentKey: "7:/etc/nginx/nginx.conf", primaryDraftSessionId: "sess-1" },
    });
    resolveConflictMock.mockResolvedValue({ status: "saved", session: makeSession() });
    await renderOpenEditor();
    typeInEditor("server {\n  listen 8080;\n}\n");

    await pressSave();

    const banner = await screen.findByTestId("remote-file-editor-conflict");
    expect(banner).toHaveTextContent("externalEdit.conflict.remoteChangedTitle");
    expect(screen.getByText("externalEdit.actions.merge")).toBeInTheDocument();
    expect(screen.getByText("externalEdit.actions.compare")).toBeInTheDocument();
    expect(screen.getByText("externalEdit.actions.reread")).toBeInTheDocument();
    // 本地改动仍在编辑器里，远端没有被写入
    expect(screen.getByTestId("remote-file-editor-content")).toHaveTextContent("listen 8080;");

    // 横幅出现之后又改了一行：覆盖写回的必须是屏幕上的内容，而不是冲突那一刻的本地副本
    typeInEditor("server {\n  listen 9090;\n}\n");
    fireEvent.click(screen.getByText("externalEdit.actions.overwrite"));
    expect(resolveConflictMock).not.toHaveBeenCalled();
    await act(async () => {
      fireEvent.click(screen.getByTestId("remote-file-editor-overwrite-confirm"));
    });

    expect(saveSessionTextMock).toHaveBeenLastCalledWith("sess-1", "server {\n  listen 9090;\n}\n");
    expect(resolveConflictMock).toHaveBeenCalledWith("sess-1", "overwrite");
    await waitFor(() => expect(screen.queryByTestId("remote-file-editor-conflict")).not.toBeInTheDocument());
    expect(screen.queryByTestId("remote-file-editor-unsaved")).not.toBeInTheDocument();
  });

  it("routes merge and diff into the existing external-edit workbenches", async () => {
    saveSessionTextMock.mockResolvedValue({
      status: "conflict_remote_changed",
      session: makeSession({ state: "conflict" }),
    });
    prepareMergeMock.mockResolvedValue({
      documentKey: "7:/etc/nginx/nginx.conf",
      primaryDraftSessionId: "sess-1",
      fileName: "nginx.conf",
      remotePath: "/etc/nginx/nginx.conf",
      localContent: "local\n",
      remoteContent: "remote\n",
      finalContent: "local\n",
      remoteHash: "remote-hash",
    });
    compareSessionMock.mockResolvedValue({
      documentKey: "7:/etc/nginx/nginx.conf",
      primaryDraftSessionId: "sess-1",
      fileName: "nginx.conf",
      remotePath: "/etc/nginx/nginx.conf",
      localContent: "local\n",
      remoteContent: "remote\n",
      readOnly: true,
    });
    await renderOpenEditor();
    typeInEditor("changed\n");
    await pressSave();

    await act(async () => {
      fireEvent.click(await screen.findByText("externalEdit.actions.merge"));
    });
    expect(prepareMergeMock).toHaveBeenCalledWith("sess-1");
    expect(await screen.findByTestId("external-edit-merge-workbench")).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByText("externalEdit.actions.compare"));
    });
    expect(compareSessionMock).toHaveBeenCalledWith("sess-1");
    expect(await screen.findByTestId("external-edit-compare-workbench")).toBeInTheDocument();
  });

  it("adopts the rebuilt session and the remote text after a reread", async () => {
    saveSessionTextMock.mockResolvedValue({
      status: "conflict_remote_changed",
      session: makeSession({ state: "conflict" }),
    });
    resolveConflictMock.mockResolvedValue({
      status: "reread",
      session: makeSession({ id: "sess-2", state: "clean" }),
    });
    await renderOpenEditor();
    typeInEditor("changed\n");
    await pressSave();

    readSessionTextMock.mockResolvedValue("remote-new\n");
    await act(async () => {
      fireEvent.click(await screen.findByText("externalEdit.actions.reread"));
    });

    expect(resolveConflictMock).toHaveBeenCalledWith("sess-1", "reread");
    await waitFor(() => expect(readSessionTextMock).toHaveBeenLastCalledWith("sess-2"));
    const tab = useTabStore.getState().tabs[0];
    expect((tab.meta as EditorTabMeta).sessionId).toBe("sess-2");
  });

  it("keeps a missing remote, a refused write and a failed rebind apart and preserves the local edit", async () => {
    await renderOpenEditor();
    typeInEditor("changed\n");

    saveSessionTextMock.mockResolvedValueOnce({
      status: "remote_missing",
      message: "远程文件不存在，请先确认是否需要重新创建远程文件",
      session: makeSession({ state: "remote_missing" }),
    });
    await pressSave();
    expect(await screen.findByText(/远程文件不存在/)).toBeInTheDocument();

    saveSessionTextMock.mockRejectedValueOnce(new Error("保存远程文件失败: permission denied"));
    await pressSave();
    expect(await screen.findByText(/permission denied/)).toBeInTheDocument();
    expect(screen.queryByText(/远程文件不存在/)).not.toBeInTheDocument();

    saveSessionTextMock.mockRejectedValueOnce(
      new Error("当前远程文件已不可访问；请先重新连接该资产的终端会话后再继续同步")
    );
    await pressSave();
    expect(await screen.findByText(/请先重新连接该资产的终端会话/)).toBeInTheDocument();
    expect(screen.queryByText(/permission denied/)).not.toBeInTheDocument();

    expect(screen.getByTestId("remote-file-editor-content")).toHaveTextContent("changed");
    expect(useTabStore.getState().unsavedTabIds).toContain("editor-sess-1");
  });

  it("asks to save, discard or cancel before closing a tab with unsaved changes", async () => {
    saveSessionTextMock.mockResolvedValue({ status: "saved", session: makeSession() });
    await renderOpenEditor();
    typeInEditor("changed\n");

    act(() => useTabStore.getState().closeTab("editor-sess-1"));

    expect(await screen.findByTestId("remote-file-editor-close-confirm")).toBeInTheDocument();
    expect(useTabStore.getState().tabs).toHaveLength(1);

    fireEvent.click(screen.getByText("action.cancel"));
    await waitFor(() => expect(screen.queryByTestId("remote-file-editor-close-confirm")).not.toBeInTheDocument());
    expect(useTabStore.getState().tabs).toHaveLength(1);

    act(() => useTabStore.getState().closeTab("editor-sess-1"));
    await act(async () => {
      fireEvent.click(await screen.findByText("externalEdit.builtIn.saveAndClose"));
    });
    expect(saveSessionTextMock).toHaveBeenCalledWith("sess-1", "changed\n");
    expect(useTabStore.getState().tabs).toHaveLength(0);
  });

  it("closes without saving when the user discards the unsaved changes", async () => {
    await renderOpenEditor();
    typeInEditor("changed\n");

    act(() => useTabStore.getState().closeTab("editor-sess-1"));
    await act(async () => {
      fireEvent.click(await screen.findByText("externalEdit.builtIn.discardAndClose"));
    });

    expect(saveSessionTextMock).not.toHaveBeenCalled();
    expect(useTabStore.getState().tabs).toHaveLength(0);
    expect(useTabStore.getState().unsavedTabIds).not.toContain("editor-sess-1");
  });

  it("keeps an unsaved editor tab out of the bulk close actions and asks about it instead", () => {
    const pageTab: Tab = { id: "page-1", type: "page", label: "settings", meta: { type: "page", pageId: "settings" } };
    const cases = [
      { bulkClose: "closeOtherTabs", tabs: [editorTab(), pageTab] },
      { bulkClose: "closeLeftTabs", tabs: [editorTab(), pageTab] },
      { bulkClose: "closeRightTabs", tabs: [pageTab, editorTab()] },
    ] as const;

    for (const { bulkClose, tabs } of cases) {
      useTabStore.setState({
        tabs: [...tabs],
        activeTabId: "page-1",
        unsavedTabIds: ["editor-sess-1"],
        pendingCloseTabId: null,
      });
      act(() => useTabStore.getState()[bulkClose]("page-1"));

      const state = useTabStore.getState();
      expect(
        state.tabs.map((tab) => tab.id),
        bulkClose
      ).toContain("editor-sess-1");
      expect(state.unsavedTabIds, bulkClose).toContain("editor-sess-1");
      expect(state.pendingCloseTabId, bulkClose).toBe("editor-sess-1");
    }
  });

  // 恢复时的重读需要一个可用会话，而恢复那一刻通常没有（决策 12）：拿到结论之前
  // tab 必须显式呈现「远端状态未确认」，而不是装成干净态，也不自己去新建连接。
  function connectTerminalForAsset(assetId: number) {
    act(() => {
      useTabStore.setState((state) => ({
        tabs: [
          ...state.tabs,
          {
            id: "term-1",
            type: "terminal",
            label: "prod-web-1",
            meta: {
              type: "terminal",
              assetId,
              assetName: "prod-web-1",
              assetIcon: "",
              host: "10.0.0.1",
              port: 22,
              username: "root",
            },
          },
        ],
      }));
      useTerminalStore.setState({
        tabData: {
          "term-1": {
            splitTree: { type: "terminal", sessionId: "ssh-1" },
            activePaneId: "ssh-1",
            panes: { "ssh-1": { sessionId: "ssh-1", transport: "ssh", connected: true, connectedAt: 1 } },
            directoryFollowMode: "off",
          },
        },
      });
    });
  }

  async function renderRestoredEditor() {
    useTabStore.setState({
      tabs: [editorTab()],
      activeTabId: "editor-sess-1",
      restoredTabIds: ["editor-sess-1"],
    });
    render(
      <StrictMode>
        <EditorHost />
      </StrictMode>
    );
    await screen.findByTestId("remote-file-editor-content");
  }

  it("presents a restored tab as unconfirmed and waits instead of opening a connection", async () => {
    await renderRestoredEditor();

    const banner = screen.getByTestId("remote-file-editor-unconfirmed");
    expect(banner).toHaveTextContent("externalEdit.builtIn.unconfirmedTitle");
    expect(banner).toHaveTextContent("externalEdit.builtIn.unconfirmedWaiting");
    expect(refreshSessionMock).not.toHaveBeenCalled();
    expect(screen.queryByTestId("remote-file-editor-conflict")).not.toBeInTheDocument();
  });

  it("re-reads once the asset gains a connected session and opens the same four exits on conflict", async () => {
    refreshSessionMock.mockResolvedValue(makeSession({ state: "conflict", dirty: true }));
    await renderRestoredEditor();
    typeInEditor("changed\n");
    expect(screen.getByTestId("remote-file-editor-unconfirmed")).toBeInTheDocument();

    connectTerminalForAsset(7);

    await waitFor(() => expect(refreshSessionMock).toHaveBeenCalledWith("sess-1"));
    expect(await screen.findByTestId("remote-file-editor-conflict")).toBeInTheDocument();
    for (const exit of ["merge", "compare", "reread", "overwrite"]) {
      expect(screen.getByText(`externalEdit.actions.${exit}`)).toBeInTheDocument();
    }
    // 结论已经拿到：不再是「未确认」，而本地改动原样留在编辑器里。
    expect(screen.queryByTestId("remote-file-editor-unconfirmed")).not.toBeInTheDocument();
    expect(screen.getByTestId("remote-file-editor-content")).toHaveTextContent("changed");
    // 一次恢复只该问远端一次：StrictMode 的二次挂载不能变成第二次重读。
    expect(refreshSessionMock).toHaveBeenCalledTimes(1);
  });

  it("drops the unconfirmed presentation when the re-read finds the remote unchanged", async () => {
    refreshSessionMock.mockResolvedValue(makeSession({ state: "clean" }));
    await renderRestoredEditor();
    expect(screen.getByTestId("remote-file-editor-unconfirmed")).toBeInTheDocument();

    connectTerminalForAsset(7);

    await waitFor(() => expect(screen.queryByTestId("remote-file-editor-unconfirmed")).not.toBeInTheDocument());
    expect(screen.queryByTestId("remote-file-editor-conflict")).not.toBeInTheDocument();
    expect(screen.queryByTestId("remote-file-editor-error")).not.toBeInTheDocument();
  });

  // 「正在重新读取远端…」只能在真的在重读时出现：保存 / 合并 / 比对同样会把 store 的
  // savingSessionId 指到本会话，拿它当在途标记会让未确认横幅谎报，还会锁死手动重读按钮。
  it("does not claim a re-read is running while a save is in flight", async () => {
    let releaseSave: (result: { status: string }) => void = () => {};
    saveSessionTextMock.mockImplementation(
      () =>
        new Promise<{ status: string }>((resolve) => {
          releaseSave = resolve;
        })
    );
    await renderRestoredEditor();
    typeInEditor("changed\n");

    fireEvent.click(screen.getByTestId("remote-file-editor-save"));
    await waitFor(() => expect(useExternalEditStore.getState().savingSessionId).toBe("sess-1"));

    const banner = screen.getByTestId("remote-file-editor-unconfirmed");
    expect(banner).toHaveTextContent("externalEdit.builtIn.unconfirmedWaiting");
    expect(banner).not.toHaveTextContent("externalEdit.builtIn.unconfirmedChecking");
    expect(screen.getByText("externalEdit.actions.refresh").closest("button")).toBeEnabled();

    await act(async () => releaseSave({ status: "saved" }));
  });

  it("re-reads on demand from the unconfirmed state", async () => {
    refreshSessionMock.mockResolvedValue(makeSession({ state: "clean" }));
    await renderRestoredEditor();

    await act(async () => {
      fireEvent.click(screen.getByText("externalEdit.actions.refresh"));
    });

    expect(refreshSessionMock).toHaveBeenCalledWith("sess-1");
    await waitFor(() => expect(screen.queryByTestId("remote-file-editor-unconfirmed")).not.toBeInTheDocument());
  });

  // 真机复现（2026-09-09 运行时验证）：恢复时的重读被会话身份校验拒绝，
  // 审计里留下了 external_edit_refresh 失败，编辑器上却什么都没出现 —— 用户以为已同步。
  it("keeps a moved remote path apart from an unreachable session and stays unconfirmed on both", async () => {
    refreshSessionMock.mockRejectedValueOnce(
      new Error("当前文件位置已变化，无法确认仍是同一份远程文件；请在同一资产中重新打开该远程文件后再继续同步")
    );
    await renderRestoredEditor();
    typeInEditor("changed\n");

    connectTerminalForAsset(7);

    expect(await screen.findByText(/当前文件位置已变化/)).toBeInTheDocument();
    // 失败不是结论：远端状态仍然未确认，本地改动仍在。
    expect(screen.getByTestId("remote-file-editor-unconfirmed")).toBeInTheDocument();
    expect(screen.getByTestId("remote-file-editor-content")).toHaveTextContent("changed");
    expect(refreshSessionMock).toHaveBeenCalledTimes(1);

    refreshSessionMock.mockRejectedValueOnce(
      new Error("当前远程文件已不可访问；请先重新连接该资产的终端会话后再继续同步")
    );
    await act(async () => {
      fireEvent.click(screen.getByText("externalEdit.actions.refresh"));
    });

    expect(await screen.findByText(/请先重新连接该资产的终端会话/)).toBeInTheDocument();
    expect(screen.queryByText(/当前文件位置已变化/)).not.toBeInTheDocument();
    expect(screen.getByTestId("remote-file-editor-unconfirmed")).toBeInTheDocument();
    expect(screen.getByTestId("remote-file-editor-content")).toHaveTextContent("changed");
    expect(useTabStore.getState().unsavedTabIds).toContain("editor-sess-1");
  });

  it("routes a vanished remote to its own banner rather than the failure one", async () => {
    refreshSessionMock.mockResolvedValue(makeSession({ state: "remote_missing", dirty: true }));
    await renderRestoredEditor();
    typeInEditor("changed\n");
    expect(screen.getByTestId("remote-file-editor-unconfirmed")).toBeInTheDocument();

    connectTerminalForAsset(7);

    expect(await screen.findByTestId("remote-file-editor-remote-missing")).toBeInTheDocument();
    expect(screen.queryByTestId("remote-file-editor-error")).not.toBeInTheDocument();
    expect(screen.queryByTestId("remote-file-editor-conflict")).not.toBeInTheDocument();
    expect(screen.getByTestId("remote-file-editor-content")).toHaveTextContent("changed");
  });

  it("does not re-read the remote for a freshly opened editor tab", async () => {
    await renderOpenEditor();

    expect(refreshSessionMock).not.toHaveBeenCalled();
  });
});
