import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { X, Table2, Code2, Database } from "lucide-react";
import { useQueryStore, type InnerTab } from "@/stores/queryStore";
import {
  ConfirmDialog,
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  useResizeHandle,
} from "@opskat/ui";
import { notifyCopied } from "@/lib/notify";
import { formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { findDatabaseDriver, useDatabaseDrivers } from "@/lib/dbDrivers";
import { DatabaseTree, DriverUnavailableNotice } from "./DatabaseTree";
import { TableDataTab } from "./TableDataTab";
import { SqlEditorTab } from "./SqlEditorTab";

interface DatabasePanelProps {
  tabId: string;
}

export function DatabasePanel({ tabId }: DatabasePanelProps) {
  const { t } = useTranslation();
  const dbState = useQueryStore((s) => s.dbStates[tabId]);
  const closeInnerTab = useQueryStore((s) => s.closeInnerTab);
  const closeOtherInnerTabs = useQueryStore((s) => s.closeOtherInnerTabs);
  const closeRightInnerTabs = useQueryStore((s) => s.closeRightInnerTabs);
  const revealTable = useQueryStore((s) => s.revealTable);
  const setActiveInnerTab = useQueryStore((s) => s.setActiveInnerTab);
  const closeBinding = useShortcutStore((s) => s.shortcuts["tab.close"]);
  const requestCloseInnerTabs = useQueryStore((s) => s.requestCloseInnerTabs);
  const forceCloseInnerTabs = useQueryStore((s) => s.forceCloseInnerTabs);
  const cancelCloseRequest = useQueryStore((s) => s.cancelCloseRequest);
  const outerCloseRequested = useTabStore((s) => s.pendingCloseTabId === tabId);
  const cancelPendingClose = useTabStore((s) => s.cancelPendingClose);

  // 外层标签因内层未提交修改被 tabStore 挂起关闭时,转成一次「关闭全部内层 + 外层」的请求,由内层守卫决定。
  useEffect(() => {
    if (!outerCloseRequested) return;
    cancelPendingClose();
    const ids = useQueryStore.getState().dbStates[tabId]?.innerTabs.map((t) => t.id) ?? [];
    requestCloseInnerTabs(tabId, ids, { closeOuter: true });
  }, [cancelPendingClose, outerCloseRequested, requestCloseInnerTabs, tabId]);

  const driver = useTabStore((s) => (s.tabs.find((tab) => tab.id === tabId)?.meta as QueryTabMeta | undefined)?.driver);
  const driversState = useDatabaseDrivers();
  const driverUnavailable = driversState.status === "ready" && !findDatabaseDriver(driversState.drivers, driver ?? "");

  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
    defaultSize: 200,
    minSize: 140,
    maxSize: 400,
    targetRef: sidebarRef,
  });

  if (!dbState) return null;

  const { innerTabs, activeInnerTabId, closeRequest } = dbState;
  const fullNameOf = (tab: InnerTab) => (tab.type === "table" ? `${tab.database}.${tab.table}` : tab.title);
  // 单个脏标签由该 TableDataTab 自己的三选项对话框处理,这里只接多个。
  const batchClose = closeRequest && closeRequest.dirtyIds.length > 1 ? closeRequest : null;
  const batchNames = batchClose ? innerTabs.filter((tab) => batchClose.dirtyIds.includes(tab.id)).map(fullNameOf) : [];

  return (
    <div className="flex h-full w-full" data-testid="database-query-panel">
      {/* Left sidebar: Database tree */}
      <div
        ref={sidebarRef}
        className="shrink-0 border-r border-border bg-sidebar h-full overflow-hidden"
        style={{ width: sidebarWidth }}
      >
        <DatabaseTree tabId={tabId} />
      </div>

      {/* Resize handle */}
      <div
        className="w-[3px] shrink-0 cursor-col-resize hover:bg-ring/40 active:bg-ring/60 transition-colors"
        onMouseDown={handleMouseDown}
      />

      {/* Right content area */}
      <div className="flex-1 min-w-0 flex flex-col h-full">
        {driverUnavailable && <DriverUnavailableNotice driver={driver} />}
        {/* Inner tab bar */}
        {!driverUnavailable && innerTabs.length > 0 && (
          <div className="flex items-center border-b border-border bg-muted/30 shrink-0 overflow-x-auto">
            {innerTabs.map((tab, index) => {
              const isActive = tab.id === activeInnerTabId;
              const fullName = fullNameOf(tab);
              return (
                <ContextMenu key={tab.id}>
                  <Tooltip>
                    <ContextMenuTrigger asChild>
                      <TooltipTrigger asChild>
                        <div
                          data-inner-tab
                          className={`flex items-center gap-1.5 px-3 py-1.5 text-xs cursor-pointer border-r border-border whitespace-nowrap select-none transition-colors duration-150 ${
                            isActive
                              ? "bg-background text-foreground"
                              : "text-muted-foreground hover:bg-background/50 hover:text-foreground"
                          }`}
                          onClick={() => setActiveInnerTab(tabId, tab.id)}
                          onAuxClick={(e) => {
                            if (e.button !== 1) return;
                            e.preventDefault();
                            closeInnerTab(tabId, tab.id);
                          }}
                        >
                          {tab.type === "table" ? (
                            <Table2 className="h-3 w-3 shrink-0" />
                          ) : (
                            <Code2 className="h-3 w-3 shrink-0" />
                          )}
                          <span className="truncate max-w-[120px]">{fullName}</span>
                          <button
                            className="ml-1 rounded-sm p-0.5 hover:bg-muted transition-colors"
                            onClick={(e) => {
                              e.stopPropagation();
                              closeInnerTab(tabId, tab.id);
                            }}
                          >
                            <X className="h-3 w-3" />
                          </button>
                        </div>
                      </TooltipTrigger>
                    </ContextMenuTrigger>
                    <TooltipContent>{fullName}</TooltipContent>
                  </Tooltip>
                  <ContextMenuContent>
                    <ContextMenuItem onClick={() => closeInnerTab(tabId, tab.id)}>
                      {t("query.innerTab.close")}
                      <ContextMenuShortcut>{formatBinding(closeBinding)}</ContextMenuShortcut>
                    </ContextMenuItem>
                    <ContextMenuItem
                      onClick={() => closeOtherInnerTabs(tabId, tab.id)}
                      disabled={innerTabs.length <= 1}
                    >
                      {t("query.innerTab.closeOthers")}
                    </ContextMenuItem>
                    <ContextMenuItem
                      onClick={() => closeRightInnerTabs(tabId, tab.id)}
                      disabled={index >= innerTabs.length - 1}
                    >
                      {t("query.innerTab.closeRight")}
                    </ContextMenuItem>
                    <ContextMenuSeparator />
                    <ContextMenuItem
                      onClick={() => {
                        void navigator.clipboard
                          .writeText(fullName)
                          .then(() => notifyCopied(t("query.innerTab.copied")));
                      }}
                    >
                      {t("query.innerTab.copyName")}
                    </ContextMenuItem>
                    {tab.type === "table" && (
                      <ContextMenuItem onClick={() => revealTable(tabId, tab.database, tab.table)}>
                        {t("query.innerTab.reveal")}
                      </ContextMenuItem>
                    )}
                  </ContextMenuContent>
                </ContextMenu>
              );
            })}
          </div>
        )}

        {/* Tab content — render all, hide inactive to preserve state */}
        <div className="flex-1 min-h-0 relative">
          {!driverUnavailable && innerTabs.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full gap-3 text-muted-foreground">
              <Database className="h-10 w-10 opacity-30" />
              <p className="text-xs">{t("query.openTable")}</p>
            </div>
          )}
          {(driverUnavailable ? [] : innerTabs).map((tab) => {
            const isActive = tab.id === activeInnerTabId;
            return (
              <div key={tab.id} className="absolute inset-0" style={{ display: isActive ? "block" : "none" }}>
                {tab.type === "table" ? (
                  <TableDataTab tabId={tabId} innerTabId={tab.id} database={tab.database} table={tab.table} />
                ) : (
                  <SqlEditorTab tabId={tabId} innerTabId={tab.id} />
                )}
              </div>
            );
          })}
        </div>
      </div>

      <ConfirmDialog
        open={batchClose !== null}
        onOpenChange={(open) => {
          if (!open) cancelCloseRequest(tabId);
        }}
        title={t("query.unsavedTabsTitle", { count: batchNames.length })}
        description={
          <>
            <p>{t("query.unsavedTabsDesc")}</p>
            <ul className="mt-2 max-h-48 overflow-auto rounded-md border border-border bg-muted/30 px-3 py-2 font-mono text-xs leading-6 select-text">
              {batchNames.map((name) => (
                <li key={name} className="truncate">
                  {name}
                </li>
              ))}
            </ul>
          </>
        }
        cancelText={t("action.cancel")}
        confirmText={t("query.unsavedEditsDiscardClose")}
        onConfirm={() => {
          if (batchClose) forceCloseInnerTabs(tabId, batchClose.ids, batchClose.closeOuter);
        }}
      />
    </div>
  );
}
