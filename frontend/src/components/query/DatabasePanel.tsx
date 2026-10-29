import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { X, Table2, Code2, Database, Columns3, SquarePen } from "lucide-react";
import { useQueryStore, type InnerTab } from "@/stores/queryStore";
import {
  Button,
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
import { toast } from "sonner";
import { notifyCopied } from "@/lib/notify";
import { formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { driverHasCapability, findDatabaseDriver, useDatabaseDrivers } from "@/lib/dbDrivers";
import { DatabaseTree, DriverUnavailableNotice } from "./DatabaseTree";
import { TableDataTab } from "./TableDataTab";
import { SqlEditorTab } from "./SqlEditorTab";
import { TableDesigner } from "./designer/TableDesigner";
import { TableCreator } from "./designer/TableCreator";
import { useCanCreateTable } from "./designer/access";
import { defaultCreateTarget, targetLabel } from "./designer/createTarget";

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

  const meta = useTabStore((s) => s.tabs.find((tab) => tab.id === tabId)?.meta as QueryTabMeta | undefined);
  const driver = meta?.driver;
  const driversState = useDatabaseDrivers();
  const descriptor =
    driversState.status === "ready" ? findDatabaseDriver(driversState.drivers, driver ?? "") : undefined;
  const driverUnavailable = driversState.status === "ready" && !descriptor;
  const canCreateTable = useCanCreateTable(descriptor, meta?.assetId);
  const openSqlTab = useQueryStore((s) => s.openSqlTab);
  const openCreateTableTab = useQueryStore((s) => s.openCreateTableTab);

  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
    defaultSize: 200,
    minSize: 140,
    maxSize: 400,
    targetRef: sidebarRef,
  });

  if (!dbState) return null;

  const { innerTabs, activeInnerTabId, closeRequest, dirtyInnerTabIds } = dbState;
  // 复制名称给出的名字:表与设计器是 db.table,新建表是目标库(db 或 db.schema),SQL 标签是标题。
  const fullNameOf = (tab: InnerTab) => {
    if (tab.type === "sql") return tab.title;
    if (tab.type === "designer" && tab.mode === "create") return targetLabel(tab);
    return `${tab.database}.${tab.table}`;
  };
  // 标签上显示的标题:设计器带上它是哪种设计器。
  const titleOf = (tab: InnerTab) => {
    if (tab.type !== "designer") return fullNameOf(tab);
    if (tab.mode === "view") return t("query.designer.tabTitle.view", { name: fullNameOf(tab) });
    if (tab.mode === "design") return t("query.designer.tabTitle.design", { name: fullNameOf(tab) });
    return t("query.designer.tabTitle.create", { name: fullNameOf(tab) });
  };
  // 空白区的「新建表」没有具体位置:建在第一个展开的库,没有时是资产的默认库。
  const emptyCreateTarget = defaultCreateTarget({
    databases: dbState.databases,
    expandedDbs: dbState.expandedDbs,
    defaultDatabase: meta?.defaultDatabase ?? "",
    schemas: driverHasCapability(descriptor, "schemas"),
  });
  // 单个脏标签由该 TableDataTab 自己的三选项对话框处理,这里只接多个。
  const batchClose = closeRequest && closeRequest.dirtyIds.length > 1 ? closeRequest : null;
  const batchNames = batchClose ? innerTabs.filter((tab) => batchClose.dirtyIds.includes(tab.id)).map(titleOf) : [];

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
              const title = titleOf(tab);
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
                          ) : tab.type === "designer" ? (
                            <Columns3 className="h-3 w-3 shrink-0" />
                          ) : (
                            <Code2 className="h-3 w-3 shrink-0" />
                          )}
                          <span className="truncate max-w-[160px]">{title}</span>
                          {dirtyInnerTabIds?.includes(tab.id) && (
                            <span
                              data-testid="inner-tab-dirty"
                              className="size-1.5 shrink-0 rounded-full bg-warning"
                              aria-hidden
                            />
                          )}
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
                    <TooltipContent>{title}</TooltipContent>
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
                        void navigator.clipboard.writeText(fullName).then(
                          () => notifyCopied(t("query.innerTab.copied")),
                          (e: unknown) => toast.error(String(e))
                        );
                      }}
                    >
                      {t("query.innerTab.copyName")}
                    </ContextMenuItem>
                    {(tab.type === "table" || (tab.type === "designer" && tab.mode !== "create")) && (
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
            <div className="flex flex-col items-center justify-center h-full gap-4 text-muted-foreground">
              <Database className="h-10 w-10 opacity-30" />
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  className="h-8 text-xs"
                  data-testid="empty-new-sql"
                  onClick={() => openSqlTab(tabId)}
                >
                  <SquarePen className="h-3.5 w-3.5" />
                  {t("query.newSql")}
                </Button>
                {canCreateTable && emptyCreateTarget && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 text-xs"
                    data-testid="empty-new-table"
                    title={t("query.newTableIn", { name: targetLabel(emptyCreateTarget) })}
                    onClick={() => openCreateTableTab(tabId, emptyCreateTarget.database, emptyCreateTarget.schema)}
                  >
                    <Table2 className="h-3.5 w-3.5" />
                    {t("query.newTable")}
                  </Button>
                )}
              </div>
            </div>
          )}
          {(driverUnavailable ? [] : innerTabs).map((tab) => {
            const isActive = tab.id === activeInnerTabId;
            return (
              <div key={tab.id} className="absolute inset-0" style={{ display: isActive ? "block" : "none" }}>
                {tab.type === "table" ? (
                  <TableDataTab tabId={tabId} innerTabId={tab.id} database={tab.database} table={tab.table} />
                ) : tab.type === "designer" ? (
                  tab.mode === "create" ? (
                    <TableCreator tabId={tabId} innerTabId={tab.id} database={tab.database} schema={tab.schema} />
                  ) : (
                    <TableDesigner
                      tabId={tabId}
                      innerTabId={tab.id}
                      database={tab.database}
                      table={tab.table}
                      mode={tab.mode}
                    />
                  )
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
