import { useState, useCallback, useRef, useEffect, lazy, Suspense } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { ThemeProvider } from "@/components/theme-provider";
import { TooltipProvider, Toaster } from "@opskat/ui";
import { Sidebar } from "@/components/layout/Sidebar";
import { AssetTree } from "@/components/layout/AssetTree";
import { MainPanel } from "@/components/layout/MainPanel";
import { SideAssistantPanel } from "@/components/ai/SideAssistantPanel";
import { WindowControls } from "@/components/layout/WindowControls";
import { TopBar } from "@/components/layout/TopBar";
import { CommandPaletteDialog } from "@/components/command/CommandPaletteDialog";
import { SnippetAssetDrawer } from "@/components/snippet/SnippetAssetDrawer";
import { EdgeRevealStrip } from "@/components/layout/EdgeRevealStrip";
import { useLayoutStore } from "@/stores/layoutStore";
import { LeftPanel } from "@/components/layout/LeftPanel";
import { SideTabList } from "@/components/layout/SideTabList";
import { PermissionDialog } from "@/components/ai/PermissionDialog";
import { OpsctlApprovalDialog } from "@/components/approval/OpsctlApprovalDialog";
import { ErrorBoundary } from "@/components/ErrorBoundary";

// 资产表单/分组对话框：用户点"添加/编辑"才会打开，从首屏 bundle 拆出。
const AssetForm = lazy(() => import("@/components/asset/AssetForm").then((m) => ({ default: m.AssetForm })));
const GroupDialog = lazy(() => import("@/components/asset/GroupDialog").then((m) => ({ default: m.GroupDialog })));

import { useAssetStore } from "@/stores/assetStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useSFTPStore } from "@/stores/sftpStore";
import { getAssetType } from "@/lib/assetTypes";
import { useTabStore } from "@/stores/tabStore";
import { useSnippetStore } from "@/stores/snippetStore";
import { bootstrapExtensions } from "@/extension/init";
import { openAssetConnection } from "@/lib/openAsset";
import { useKeyboardShortcuts } from "@/hooks/useKeyboardShortcuts";
import { useExternalEditStore } from "@/stores/externalEditStore";
import { asset_entity, group_entity } from "../wailsjs/go/models";
import { EventsOn, WindowToggleMaximise } from "../wailsjs/runtime/runtime";

function App() {
  const { t } = useTranslation();

  // 异步加载数据，不阻塞首屏渲染
  useEffect(() => {
    bootstrapExtensions().catch((err) => console.error("Extension bootstrap failed:", err));
    useAssetStore
      .getState()
      .fetchAssets()
      .catch((err) => console.error("Fetch assets failed:", err));
    useAssetStore
      .getState()
      .fetchGroups()
      .catch((err) => console.error("Fetch groups failed:", err));
    useExternalEditStore
      .getState()
      .fetchSessions()
      .catch((err) => console.error("Fetch external edit sessions failed:", err));
  }, []);

  // 监听外部数据变更（opsctl 等），自动刷新 UI
  useEffect(() => {
    const cancel = EventsOn("data:changed", () => {
      useAssetStore.getState().refresh();
    });
    return () => {
      cancel();
    };
  }, []);

  // 监听自动更新检查结果，以及内置工具（opsctl / Skill）重装失败。
  // 重装在启动时由后端 updateInstalledTools 触发，此时设置页未必挂载，所以这两条
  // 失败事件必须全局监听（不能只放在 UpdateSection），否则失败对用户完全无感知。
  useEffect(() => {
    const cancelAvailable = EventsOn("update:available", (info: { latestVersion: string }) => {
      toast.info(t("appUpdate.autoUpdateFound", { version: info.latestVersion }));
    });
    const cancelOpsctlErr = EventsOn("update:opsctl-error", (errMsg: string) => {
      toast.error(t("appUpdate.opsctlUpdateFailed", { error: errMsg }));
    });
    const cancelSkillErr = EventsOn("update:skill-error", (errMsg: string) => {
      toast.error(t("appUpdate.skillUpdateFailed", { error: errMsg }));
    });
    return () => {
      cancelAvailable();
      cancelOpsctlErr();
      cancelSkillErr();
    };
  }, [t]);

  // 监听系统启动状态
  useEffect(() => {
    const cancel = EventsOn("system:status", (entries: Array<{ level: string }>) => {
      if (!entries || entries.length === 0) return;
      const hasError = entries.some((e) => e.level === "error");
      const message = hasError ? t("systemStatus.toastError") : t("systemStatus.toastWarn");
      const toastFn = hasError ? toast.error : toast.warning;
      toastFn(message, {
        action: {
          label: t("systemStatus.showDetail"),
          onClick: () => {
            const tabStore = useTabStore.getState();
            const existing = tabStore.tabs.find((tab) => tab.id === "settings");
            if (existing) {
              tabStore.activateTab("settings");
            } else {
              tabStore.openTab({
                id: "settings",
                type: "page",
                label: "settings",
                meta: { type: "page", pageId: "settings" },
              });
            }
          },
        },
      });
    });
    return () => cancel();
  }, [t]);

  useEffect(() => {
    const cancel = EventsOn("external-edit:event", (event: unknown) => {
      useExternalEditStore.getState().applyEvent(event as never);
    });
    return () => cancel();
  }, []);

  // 双击拖拽区域最大化/还原窗口
  useEffect(() => {
    const handleDblClick = (e: MouseEvent) => {
      let el = e.target as HTMLElement | null;
      while (el) {
        const drag = getComputedStyle(el).getPropertyValue("--wails-draggable").trim();
        if (drag === "no-drag") return;
        if (drag === "drag") {
          WindowToggleMaximise();
          return;
        }
        el = el.parentElement;
      }
    };
    window.addEventListener("dblclick", handleDblClick);
    return () => window.removeEventListener("dblclick", handleDblClick);
  }, []);

  const [sidebarHidden, setSidebarHidden] = useState(() => localStorage.getItem("sidebar_hidden") === "true");
  const [assetTreeCollapsed, setAssetTreeCollapsed] = useState(
    () => localStorage.getItem("sidebar_collapsed") === "true"
  );
  const [aiPanelCollapsed, setAiPanelCollapsed] = useState(() => localStorage.getItem("ai_panel_collapsed") === "true");
  const [commandOpen, setCommandOpen] = useState(false);
  // Snippet chosen in the command palette that needs a host picked before it runs.
  const snippetRunTarget = useSnippetStore((s) => s.runTarget);
  const clearSnippetHostPick = useSnippetStore((s) => s.clearHostPick);
  const [assetTreeWidth, setAssetTreeWidth] = useState(() => {
    const saved = localStorage.getItem("asset_tree_width");
    return saved ? Math.max(160, Math.min(480, Number(saved))) : 224;
  });
  const [assetTreeResizing, setAssetTreeResizing] = useState(false);
  const assetTreeWidthRef = useRef(assetTreeWidth);

  const toggleAIPanel = useCallback(() => {
    setAiPanelCollapsed((prev) => {
      localStorage.setItem("ai_panel_collapsed", String(!prev));
      return !prev;
    });
  }, []);

  const toggleSidebar = useCallback(() => {
    setAssetTreeCollapsed((prev) => {
      localStorage.setItem("sidebar_collapsed", String(!prev));
      return !prev;
    });
  }, []);

  const toggleSidebarHidden = useCallback(() => {
    setSidebarHidden((prev) => {
      localStorage.setItem("sidebar_hidden", String(!prev));
      return !prev;
    });
  }, []);

  const handleAssetTreeResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setAssetTreeResizing(true);
    const startX = e.clientX;
    const startWidth = assetTreeWidthRef.current;

    const onMouseMove = (ev: MouseEvent) => {
      const newWidth = Math.max(160, Math.min(480, startWidth + ev.clientX - startX));
      assetTreeWidthRef.current = newWidth;
      setAssetTreeWidth(newWidth);
    };

    const onMouseUp = () => {
      setAssetTreeResizing(false);
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
      localStorage.setItem("asset_tree_width", String(assetTreeWidthRef.current));
    };

    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }, []);

  const toggleCommandPalette = useCallback(() => {
    setCommandOpen((prev) => !prev);
  }, []);

  useKeyboardShortcuts({
    onToggleAIPanel: toggleAIPanel,
    onToggleSidebar: toggleSidebar,
    onToggleCommandPalette: toggleCommandPalette,
  });

  // 资产表单
  const [assetFormOpen, setAssetFormOpen] = useState(false);
  const [editingAsset, setEditingAsset] = useState<asset_entity.Asset | null>(null);
  const [defaultGroupId, setDefaultGroupId] = useState(0);

  // 分组对话框
  const [groupDialogOpen, setGroupDialogOpen] = useState(false);
  const [editingGroup, setEditingGroup] = useState<group_entity.Group | null>(null);

  const { selectAsset, selectGroup, deleteAsset, getAsset } = useAssetStore();
  const { connect } = useTerminalStore();

  const handleAddAsset = (groupId?: number) => {
    setEditingAsset(null);
    setDefaultGroupId(groupId ?? 0);
    setAssetFormOpen(true);
  };

  const handleEditAsset = (asset: asset_entity.Asset) => {
    setEditingAsset(asset);
    setAssetFormOpen(true);
  };

  const handleCopyAsset = async (asset: asset_entity.Asset) => {
    try {
      const fullAsset = await getAsset(asset.ID);
      const copied = new asset_entity.Asset({
        ...fullAsset,
        ID: 0,
        Name: `${fullAsset.Name} - 副本`,
      });
      setEditingAsset(copied);
      setAssetFormOpen(true);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleSelectAsset = (asset: asset_entity.Asset) => {
    selectAsset(asset.ID);
  };

  const handleOpenInfoTab = useCallback((type: "asset" | "group", id: number, name: string, icon?: string) => {
    const tabStore = useTabStore.getState();
    const infoTabId = `info-${type}-${id}`;
    const existing = tabStore.tabs.find((t) => t.id === infoTabId);
    if (existing) {
      tabStore.activateTab(infoTabId);
    } else {
      tabStore.openTab({
        id: infoTabId,
        type: "info",
        label: name,
        icon,
        meta: { type: "info", targetType: type, targetId: id, name, icon },
      });
    }
  }, []);

  const handleDeleteAsset = async (id: number) => {
    await deleteAsset(id);
  };

  const handleConnectAsset = async (asset: asset_entity.Asset) => {
    await openAssetConnection(asset);
  };

  const handleConnectAssetInNewTab = async (asset: asset_entity.Asset) => {
    const def = getAssetType(asset.Type);
    if (!def?.canConnectInNewTab) return;
    if (def.connectAction === "page" && def.pageId) {
      useTabStore.getState().openTab({
        id: `${def.pageId}-${asset.ID}-${Date.now()}`,
        type: "page",
        label: asset.Name,
        icon: asset.Icon || def.pageIcon,
        meta: { type: "page", pageId: def.pageId, assetId: asset.ID },
      });
      return;
    }
    try {
      await connect(asset, "", true);
    } catch (e) {
      toast.error(`${asset.Name}: ${String(e)}`);
    }
  };

  const handleOpenFileManager = async (asset: asset_entity.Asset) => {
    if (asset.Type !== "ssh") return;
    try {
      const tabId = await connect(asset);
      if (!tabId) return;
      const sftp = useSFTPStore.getState();
      if (!sftp.fileManagerOpenTabs[tabId]) {
        sftp.toggleFileManager(tabId);
      }
    } catch (e) {
      toast.error(`${asset.Name}: ${String(e)}`);
    }
  };

  // Sidebar page navigation
  const handlePageChange = useCallback((page: string) => {
    const tabStore = useTabStore.getState();
    if (page === "home") {
      const homeTab = tabStore.tabs.find((t) => t.type === "terminal" || t.type === "info" || t.type === "query");
      tabStore.activateTab(homeTab?.id || tabStore.tabs[0]?.id || "");
      return;
    }
    // Page tabs: settings, forward, sshkeys, audit, snippets
    const existing = tabStore.tabs.find((t) => t.id === page);
    if (existing) {
      tabStore.activateTab(page);
    } else {
      tabStore.openTab({
        id: page,
        type: "page",
        label: page,
        meta: { type: "page", pageId: page },
      });
    }
  }, []);

  const tabBarLayout = useLayoutStore((s) => s.tabBarLayout);
  const leftPanelVisible = useLayoutStore((s) => s.leftPanelVisible);
  const activeSidePanel = useLayoutStore((s) => s.activeSidePanel);

  // Derive active page for sidebar highlighting
  const activeTab = useTabStore((s) => s.tabs.find((t) => t.id === s.activeTabId));
  const activePage = activeTab?.type === "page" ? activeTab.id : "home";

  // 顶部「隐藏资产列表」按钮：根据 tab 布局适配不同的可见状态
  const assetTreeIsCollapsed = tabBarLayout === "left" ? !leftPanelVisible : assetTreeCollapsed;
  const handleToggleAssetTree = useCallback(() => {
    if (tabBarLayout === "left") {
      useLayoutStore.getState().toggleVisible();
    } else {
      toggleSidebar();
    }
  }, [tabBarLayout, toggleSidebar]);

  return (
    <ThemeProvider defaultTheme="system">
      <ErrorBoundary>
        <TooltipProvider>
          <div className="flex h-screen w-screen flex-col overflow-hidden bg-background" data-testid="app-root">
            {!sidebarHidden && (
              <TopBar
                commandOpen={commandOpen}
                onCommandOpenChange={setCommandOpen}
                onConnectAsset={handleConnectAsset}
                assetTreeCollapsed={assetTreeIsCollapsed}
                onToggleAssetTree={handleToggleAssetTree}
                aiPanelCollapsed={aiPanelCollapsed}
                onToggleAIPanel={toggleAIPanel}
              />
            )}
            {sidebarHidden && (
              <CommandPaletteDialog
                open={commandOpen}
                onOpenChange={setCommandOpen}
                onConnectAsset={handleConnectAsset}
              />
            )}
            <WindowControls />
            <div className="flex flex-1 min-h-0 overflow-hidden">
              {tabBarLayout === "left" ? (
                <>
                  {sidebarHidden && <EdgeRevealStrip onClick={toggleSidebarHidden} />}
                  {!sidebarHidden && (
                    <Sidebar
                      activePage={activePage}
                      onPageChange={handlePageChange}
                      onHideSidebar={toggleSidebarHidden}
                    />
                  )}
                  {leftPanelVisible && (
                    <LeftPanel>
                      {activeSidePanel === "assets" ? (
                        <AssetTree
                          collapsed={false}
                          sidebarHidden={sidebarHidden}
                          onShowSidebar={toggleSidebarHidden}
                          onAddAsset={handleAddAsset}
                          onAddGroup={() => {
                            setEditingGroup(null);
                            setGroupDialogOpen(true);
                          }}
                          onEditGroup={(group) => {
                            setEditingGroup(group);
                            setGroupDialogOpen(true);
                          }}
                          onGroupDetail={(group) => {
                            selectGroup(group.ID);
                            selectAsset(null);
                            handleOpenInfoTab("group", group.ID, group.Name, group.Icon || undefined);
                          }}
                          onEditAsset={handleEditAsset}
                          onCopyAsset={handleCopyAsset}
                          onConnectAsset={handleConnectAsset}
                          onConnectAssetInNewTab={handleConnectAssetInNewTab}
                          onOpenFileManager={handleOpenFileManager}
                          onSelectAsset={handleSelectAsset}
                          onOpenInfoTab={handleOpenInfoTab}
                        />
                      ) : (
                        <SideTabList />
                      )}
                    </LeftPanel>
                  )}
                </>
              ) : (
                <>
                  {sidebarHidden && <EdgeRevealStrip onClick={toggleSidebarHidden} />}
                  {!sidebarHidden && (
                    <Sidebar
                      activePage={activePage}
                      onPageChange={handlePageChange}
                      onHideSidebar={toggleSidebarHidden}
                    />
                  )}
                  <div
                    className="relative overflow-hidden shrink-0 transition-[width] duration-200"
                    style={{ width: assetTreeCollapsed ? 0 : assetTreeWidth }}
                  >
                    <AssetTree
                      collapsed={false}
                      sidebarHidden={sidebarHidden}
                      onShowSidebar={toggleSidebarHidden}
                      onAddAsset={handleAddAsset}
                      onAddGroup={() => {
                        setEditingGroup(null);
                        setGroupDialogOpen(true);
                      }}
                      onEditGroup={(group) => {
                        setEditingGroup(group);
                        setGroupDialogOpen(true);
                      }}
                      onGroupDetail={(group) => {
                        selectGroup(group.ID);
                        selectAsset(null);
                        handleOpenInfoTab("group", group.ID, group.Name, group.Icon || undefined);
                      }}
                      onEditAsset={handleEditAsset}
                      onCopyAsset={handleCopyAsset}
                      onConnectAsset={handleConnectAsset}
                      onConnectAssetInNewTab={handleConnectAssetInNewTab}
                      onOpenFileManager={handleOpenFileManager}
                      onSelectAsset={handleSelectAsset}
                      onOpenInfoTab={handleOpenInfoTab}
                    />
                    {/* Resize handle */}
                    {!assetTreeCollapsed && (
                      <div
                        className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize z-10 hover:bg-primary/20 active:bg-primary/30 transition-colors"
                        onMouseDown={handleAssetTreeResizeStart}
                      />
                    )}
                  </div>
                  {assetTreeResizing && <div className="fixed inset-0 z-50 cursor-col-resize" />}
                </>
              )}
              <MainPanel
                onEditAsset={handleEditAsset}
                onDeleteAsset={handleDeleteAsset}
                onConnectAsset={handleConnectAsset}
                topBarHidden={sidebarHidden}
              />
              <SideAssistantPanel collapsed={aiPanelCollapsed} onToggle={toggleAIPanel} />
              {aiPanelCollapsed && <EdgeRevealStrip side="right" onClick={toggleAIPanel} />}
            </div>
          </div>

          <Suspense fallback={null}>
            {assetFormOpen && (
              <AssetForm
                open={assetFormOpen}
                onOpenChange={setAssetFormOpen}
                editAsset={editingAsset}
                defaultGroupId={defaultGroupId}
              />
            )}
            {groupDialogOpen && (
              <GroupDialog open={groupDialogOpen} onOpenChange={setGroupDialogOpen} editGroup={editingGroup} />
            )}
          </Suspense>
          <PermissionDialog />
          <OpsctlApprovalDialog />
          {snippetRunTarget && <SnippetAssetDrawer snippet={snippetRunTarget} onClose={clearSnippetHostPick} />}
          <Toaster richColors />
        </TooltipProvider>
      </ErrorBoundary>
    </ThemeProvider>
  );
}

export default App;
