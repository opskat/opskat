import React, { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useFullscreen } from "@/hooks/useFullscreen";
import {
  ChevronRight,
  ChevronDown,
  Folder,
  FolderOpen,
  Server,
  Plus,
  FolderPlus,
  Search,
  Loader2,
  Eye,
  ArrowUp,
  ArrowDown,
  ChevronsUp,
  Pencil,
  Copy,
  Link2,
  Trash2,
  TerminalSquare,
  ExternalLink,
  Settings2,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  Button,
  ScrollArea,
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogCancel,
  AlertDialogAction,
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
} from "@opskat/ui";
import { EntityIcon } from "@/components/asset/AssetIcon";
import { filterAssets } from "@/lib/assetSearch";
import {
  buildTreeIndex,
  flattenTree,
  computeInsertionPoint,
  insertionToReorderArgs,
  rowKey,
  useAssetTreeDndStore,
  type InsertionPoint,
  type Row,
  type RowRect,
} from "@/lib/assetTreeDnd";
import { reorderAssetsOptimistically } from "@/lib/assetTreeReorderOptimistic";
import { writeAssetMarkdownRef } from "@/lib/assetRef";
import { notifyCopied } from "@/lib/notify";
import { getAssetType } from "@/lib/assetTypes";
import { useAssetTypeOptions, matchSelectedTypes } from "@/lib/assetTypes/options";
import { AssetTypeFilterButton } from "@/components/asset/AssetTypeFilterButton";
import { useAssetStore } from "@/stores/assetStore";
import { formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useActiveAssetIds } from "@/hooks/useActiveAssetIds";
import { MoveAsset } from "../../../wailsjs/go/system/System";
import { MoveGroup, ReorderAsset, ReorderGroup } from "../../../wailsjs/go/system/System";
import { asset_entity, group_entity } from "../../../wailsjs/go/models";
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useDraggable,
  useSensor,
  useSensors,
  type DragCancelEvent,
  type DragEndEvent,
  type DragMoveEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { useSortable } from "@dnd-kit/sortable";
import { defaultRangeExtractor, useVirtualizer, type Range } from "@tanstack/react-virtual";
import { errorMessage } from "@/lib/errorMessage";

interface AssetTreeProps {
  collapsed: boolean;
  sidebarHidden?: boolean;
  onShowSidebar?: () => void;
  onAddAsset: (groupId?: number) => void;
  onAddGroup: () => void;
  onEditGroup: (group: group_entity.Group) => void;
  onGroupDetail: (group: group_entity.Group) => void;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onCopyAsset: (asset: asset_entity.Asset) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onConnectAssetInNewTab?: (asset: asset_entity.Asset) => void;
  onOpenFileManager?: (asset: asset_entity.Asset) => void;
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onOpenInfoTab?: (type: "asset" | "group", id: number, name: string, icon?: string) => void;
}

const FILTER_LS_KEY = "asset_tree_type_filter";
const HIDE_EMPTY_LS_KEY = "asset_tree_hide_empty_groups";

function loadFilter(): string[] {
  try {
    const raw = localStorage.getItem(FILTER_LS_KEY);
    if (!raw) return [];
    if (raw === '"all"' || raw === "all") return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed) && parsed.every((v) => typeof v === "string")) {
      return parsed as string[];
    }
    return [];
  } catch {
    return [];
  }
}

function saveFilter(value: string[]) {
  localStorage.setItem(FILTER_LS_KEY, JSON.stringify(value));
}

function loadHideEmpty(): boolean {
  return localStorage.getItem(HIDE_EMPTY_LS_KEY) === "true";
}

function saveHideEmpty(value: boolean) {
  localStorage.setItem(HIDE_EMPTY_LS_KEY, value ? "true" : "false");
}

// Controls inside the sidebar keep their own focus and behaviour (the search field keeps
// native copy). Any other press — an asset row, a group row, empty space — makes the
// sidebar the focused surface, which is what grants the asset-reference shortcut.
const SIDEBAR_INTERACTIVE_SELECTOR = "input, textarea, select, button, a[href], [contenteditable]";

function focusSidebarUnlessInteractive(e: React.MouseEvent<HTMLDivElement>) {
  const target = e.target as HTMLElement | null;
  if (target?.closest(SIDEBAR_INTERACTIVE_SELECTOR)) return;
  e.currentTarget.focus({ preventScroll: true });
}

function afterDragCleanupFrame() {
  return new Promise<void>((resolve) => {
    if (typeof window !== "undefined" && window.requestAnimationFrame) {
      window.requestAnimationFrame(() => resolve());
      return;
    }
    setTimeout(resolve, 0);
  });
}

function projectIndicator(
  point: InsertionPoint,
  rowsList: Row[],
  rects: Map<string, RowRect>
): { y: number | null; depth: number | null } {
  if (point.kind === "invalid") {
    return { y: null, depth: null };
  }
  if (point.kind === "root-end") {
    if (rowsList.length > 0) {
      const last = rowsList[rowsList.length - 1];
      const rect = rects.get(rowKey(last));
      if (rect) return { y: rect.bottom, depth: 0 };
    }
    return { y: null, depth: null };
  }
  if (point.kind === "before-asset" || point.kind === "before-group") {
    const target =
      point.kind === "before-asset"
        ? rowsList.find((r) => r.kind === "asset" && r.assetID === point.assetID)
        : rowsList.find((r) => r.kind === "group-header" && r.groupID === point.groupID);
    if (!target) return { y: null, depth: null };
    const rect = rects.get(rowKey(target));
    if (!rect) return { y: null, depth: null };
    return { y: rect.top, depth: point.depth };
  }
  if (point.kind === "after-asset") {
    const target = rowsList.find((r) => r.kind === "asset" && r.assetID === point.assetID);
    if (!target) return { y: null, depth: null };
    const rect = rects.get(rowKey(target));
    if (!rect) return { y: null, depth: null };
    return { y: rect.bottom, depth: point.depth };
  }
  // into-group-first
  const target = rowsList.find((r) => r.kind === "group-header" && r.groupID === point.groupID);
  if (!target) return { y: null, depth: null };
  const rect = rects.get(rowKey(target));
  if (!rect) return { y: null, depth: null };
  return { y: rect.bottom, depth: point.depth + 1 };
}

function DropIndicator() {
  const y = useAssetTreeDndStore((s) => s.indicatorY);
  const depth = useAssetTreeDndStore((s) => s.indicatorDepth);
  if (y === null) return null;
  return (
    <div
      className="pointer-events-none absolute right-2 z-20 h-px bg-primary transition-[top,left] duration-100 ease-out"
      style={{
        top: y - 0.5,
        left: `${20 + (depth ?? 0) * 12}px`,
      }}
    >
      <span className="absolute -left-1 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-primary" />
    </div>
  );
}

function parseActive(id: string): { kind: "asset" | "group"; id: number } | null {
  const m = /^(asset|group)-(\d+)$/.exec(id);
  if (!m) return null;
  return { kind: m[1] as "asset" | "group", id: Number(m[2]) };
}

export function AssetTree({
  collapsed,
  sidebarHidden,
  onShowSidebar,
  onAddAsset,
  onAddGroup,
  onEditGroup,
  onGroupDetail,
  onEditAsset,
  onCopyAsset,
  onConnectAsset,
  onConnectAssetInNewTab,
  onOpenFileManager,
  onSelectAsset,
  onOpenInfoTab,
}: AssetTreeProps) {
  const { t } = useTranslation();
  const isFullscreen = useFullscreen();
  const assets = useAssetStore((s) => s.assets);
  const groups = useAssetStore((s) => s.groups);
  const collapsedGroupIds = useAssetStore((s) => s.collapsedGroupIds);
  const fetchAssets = useAssetStore((s) => s.fetchAssets);
  const fetchGroups = useAssetStore((s) => s.fetchGroups);
  const deleteAsset = useAssetStore((s) => s.deleteAsset);
  const deleteGroup = useAssetStore((s) => s.deleteGroup);
  const refresh = useAssetStore((s) => s.refresh);
  const connectingAssetIds = useTerminalStore((s) => s.connectingAssetIds);
  const activeAssetIds = useActiveAssetIds();
  const [filter, setFilter] = useState("");
  const [selectedTypes, setSelectedTypes] = useState<string[]>(loadFilter);
  const [hideEmptyGroups, setHideEmptyGroups] = useState<boolean>(loadHideEmpty);
  const [deleteConfirm, setDeleteConfirm] = useState<{
    id: number;
    assetCount: number;
    childGroupCount: number;
  } | null>(null);
  const [deleteAssetConfirm, setDeleteAssetConfirm] = useState<asset_entity.Asset | null>(null);
  const [groupContextMenu, setGroupContextMenu] = useState<{
    group: group_entity.Group;
    position: { x: number; y: number };
  } | null>(null);
  const [dragPreviewAsset, setDragPreviewAsset] = useState<asset_entity.Asset | null>(null);
  // Row being dragged: kept mounted by the virtualizer even when scrolled out of view.
  const [draggingRowKey, setDraggingRowKey] = useState<string | null>(null);

  useEffect(() => {
    fetchAssets();
    fetchGroups();
  }, [fetchAssets, fetchGroups]);

  useEffect(() => {
    saveFilter(selectedTypes);
  }, [selectedTypes]);

  useEffect(() => {
    saveHideEmpty(hideEmptyGroups);
  }, [hideEmptyGroups]);

  const typeOptions = useAssetTypeOptions();

  const filteredAssets = useMemo(() => {
    const typeFilteredAssets = matchSelectedTypes(assets, selectedTypes, typeOptions);
    return filter
      ? filterAssets(typeFilteredAssets, groups, { query: filter }).map((r) => r.asset)
      : typeFilteredAssets;
  }, [assets, selectedTypes, typeOptions, filter, groups]);

  const hasSearchFilter = filter.trim().length > 0;
  const hasTypeFilter = selectedTypes.length > 0;
  const shouldHideEmptyGroups = hideEmptyGroups || hasSearchFilter || hasTypeFilter;

  const treeIndex = useMemo(() => buildTreeIndex(groups, filteredAssets), [groups, filteredAssets]);
  const groupById = useMemo(() => new Map(groups.map((g) => [g.ID, g] as const)), [groups]);
  const assetById = useMemo(() => new Map(filteredAssets.map((a) => [a.ID, a] as const)), [filteredAssets]);
  const ungroupedGroup = useMemo(() => new group_entity.Group({ ID: 0, Name: t("asset.ungrouped") }), [t]);

  const isFilteredEmpty = filteredAssets.length === 0 && (hasSearchFilter || hasTypeFilter);

  const handleDeleteGroup = useCallback(
    (id: number) => {
      const directAssetCount = treeIndex.assetsByGroup.get(id)?.length ?? 0;
      const childGroupCount = treeIndex.descendantGroupCounts.get(id) ?? 0;
      if (directAssetCount > 0 || childGroupCount > 0) {
        setDeleteConfirm({ id, assetCount: directAssetCount, childGroupCount });
      } else {
        deleteGroup(id, false).catch((e) => toast.error(errorMessage(e)));
      }
    },
    [treeIndex, deleteGroup]
  );

  const handleMoveAsset = useCallback(
    async (id: number, direction: string) => {
      try {
        await MoveAsset(id, direction);
        await refresh();
      } catch (e) {
        toast.error(errorMessage(e));
      }
    },
    [refresh]
  );

  const handleMoveGroup = useCallback(
    async (id: number, direction: string) => {
      try {
        await MoveGroup(id, direction);
        await refresh();
      } catch (e) {
        toast.error(errorMessage(e));
      }
    },
    [refresh]
  );

  // 5px 移动门槛 → 单击/双击不被误识别成拖动
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

  const toggleGroupCollapsed = useAssetStore((s) => s.toggleGroupCollapsed);

  const hoverExpandTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const hoverExpandTargetRef = useRef<number | null>(null);
  const rowsRef = useRef<Row[]>([]);
  const treeContainerRef = useRef<HTMLDivElement>(null);

  const rows = useMemo(
    () =>
      flattenTree({
        index: treeIndex,
        collapsedGroupIDs: new Set(collapsedGroupIds),
        shouldHideEmpty: shouldHideEmptyGroups,
      }),
    [treeIndex, collapsedGroupIds, shouldHideEmptyGroups]
  );
  useEffect(() => {
    rowsRef.current = rows;
  }, [rows]);
  const treeIsEmpty = rows.length === 0;

  const collectRowRects = useCallback((): Map<string, RowRect> => {
    const m = new Map<string, RowRect>();
    const container = treeContainerRef.current ?? document;
    const nodes = container.querySelectorAll<HTMLElement>("[data-asset-tree-row]");
    for (const node of nodes) {
      const key = node.getAttribute("data-asset-tree-row");
      if (!key) continue;
      const rect = node.getBoundingClientRect();
      m.set(key, { top: rect.top, bottom: rect.bottom, height: rect.height });
    }
    return m;
  }, []);

  const clearHoverExpand = useCallback(() => {
    if (hoverExpandTimerRef.current) {
      clearTimeout(hoverExpandTimerRef.current);
      hoverExpandTimerRef.current = null;
    }
    hoverExpandTargetRef.current = null;
  }, []);

  const handleDragStart = (e: DragStartEvent) => {
    setDraggingRowKey(String(e.active.id));
    const active = parseActive(String(e.active.id));
    if (active?.kind === "asset") {
      setDragPreviewAsset(assets.find((asset) => asset.ID === active.id) ?? null);
    } else {
      setDragPreviewAsset(null);
    }
  };

  const handleDragMove = (e: DragMoveEvent) => {
    const active = parseActive(String(e.active.id));
    if (!active) return;
    const activatorEvent = e.activatorEvent as PointerEvent;
    const pointerY = activatorEvent.clientY + e.delta.y;
    const rects = collectRowRects();

    const point = computeInsertionPoint({
      rows: rowsRef.current,
      rowRects: rects,
      pointerY,
      active,
      groups,
    });

    const containerTop = treeContainerRef.current?.getBoundingClientRect().top ?? 0;
    const isHighlight = point.kind === "into-group-first";
    if (isHighlight) {
      useAssetTreeDndStore.getState().setIndicator(point, null, null, point.groupID);
    } else {
      const { y, depth } = projectIndicator(point, rowsRef.current, rects);
      const relY = y === null ? null : y - containerTop;
      useAssetTreeDndStore.getState().setIndicator(point, relY, depth, null);
    }

    // 折叠 group hover 500ms 自动展开
    const hoverGroupID = point.kind === "before-group" || point.kind === "into-group-first" ? point.groupID : null;
    const hoverRow =
      hoverGroupID !== null
        ? rowsRef.current.find((r) => r.kind === "group-header" && r.groupID === hoverGroupID)
        : null;
    if (
      hoverRow &&
      hoverRow.kind === "group-header" &&
      hoverRow.collapsed &&
      hoverExpandTargetRef.current !== hoverGroupID
    ) {
      clearHoverExpand();
      hoverExpandTargetRef.current = hoverGroupID;
      hoverExpandTimerRef.current = setTimeout(() => {
        toggleGroupCollapsed(hoverGroupID!);
        hoverExpandTimerRef.current = null;
      }, 500);
    } else if (!hoverRow || hoverRow.kind !== "group-header" || !hoverRow.collapsed) {
      clearHoverExpand();
    }
  };

  const handleDragCancel = (_e: DragCancelEvent) => {
    setDragPreviewAsset(null);
    setDraggingRowKey(null);
    useAssetTreeDndStore.getState().clear();
    clearHoverExpand();
  };

  const handleDragEnd = async (e: DragEndEvent) => {
    setDragPreviewAsset(null);
    setDraggingRowKey(null);
    clearHoverExpand();
    const point = useAssetTreeDndStore.getState().point;
    useAssetTreeDndStore.getState().clear();
    if (!point) return;

    const active = parseActive(String(e.active.id));
    if (!active) return;

    const args = insertionToReorderArgs({ point, active, groups, assets });
    if (!args) return;

    let appliedOptimistic = false;
    try {
      if (args.kind === "asset") {
        await afterDragCleanupFrame();
        useAssetStore.setState((state) => ({
          assets: reorderAssetsOptimistically(state.assets, args.id, args.targetGroupID, args.beforeID),
        }));
        appliedOptimistic = true;
        await ReorderAsset(args.id, args.targetGroupID, args.beforeID);
      } else {
        await ReorderGroup(args.id, args.targetParentID, args.beforeID);
      }
      await refresh();
    } catch (err) {
      if (appliedOptimistic) void refresh();
      toast.error(errorMessage(err));
    }
  };

  const handleConfirmDelete = async (deleteAssets: boolean) => {
    if (!deleteConfirm) return;
    try {
      await deleteGroup(deleteConfirm.id, deleteAssets);
    } catch (e) {
      toast.error(errorMessage(e));
    }
    setDeleteConfirm(null);
  };

  const handleGroupContextMenuClose = useCallback(() => setGroupContextMenu(null), []);

  const handleGroupContextMenu = useCallback((group: group_entity.Group, event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    setGroupContextMenu({ group, position: { x: event.clientX, y: event.clientY } });
  }, []);

  const renderGroupContextMenu = (group: group_entity.Group) => (
    <GroupContextMenuContent
      group={group}
      t={t}
      onAddAsset={onAddAsset}
      onGroupDetail={onGroupDetail}
      onEditGroup={onEditGroup}
      onMoveGroup={handleMoveGroup}
      onDeleteGroup={handleDeleteGroup}
      onClose={handleGroupContextMenuClose}
    />
  );

  if (collapsed) return null;

  return (
    <div
      data-testid="asset-tree"
      data-asset-sidebar=""
      tabIndex={-1}
      onMouseDown={focusSidebarUnlessInteractive}
      className="flex h-full w-full flex-col border-r border-panel-divider bg-sidebar outline-none"
    >
      {/* Drag region for frameless window */}
      <div
        className={`${isFullscreen ? "h-0" : "h-8"} w-full shrink-0`}
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
      />
      <div className="flex flex-col gap-1.5 px-3 pb-2 border-b border-panel-divider">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1">
            {sidebarHidden && (
              <Button
                variant="ghost"
                size="icon"
                className="h-6 w-6"
                onClick={onShowSidebar}
                title={t("panel.showSidebar")}
              >
                <Eye className="h-3.5 w-3.5" />
              </Button>
            )}
            <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              {t("asset.title")}
            </span>
          </div>
          <div className="flex gap-0.5">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6"
                  onClick={() => onAddGroup()}
                  aria-label={t("asset.addGroup")}
                >
                  <FolderPlus className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">{t("asset.addGroup")}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6"
                  onClick={() => onAddAsset()}
                  aria-label={t("asset.addAsset")}
                  data-testid="add-asset-button"
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">{t("asset.addAsset")}</TooltipContent>
            </Tooltip>
          </div>
        </div>
        <div className="flex items-center gap-1">
          <div className="relative flex-1">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground" />
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder={t("asset.search")}
              className="h-7 w-full rounded-md border border-sidebar-border bg-sidebar pl-7 pr-2 text-xs outline-none focus-visible:border-ring/70 focus-visible:ring-1 focus-visible:ring-ring/45 placeholder:text-muted-foreground/60 transition-colors duration-150"
            />
          </div>
          <AssetTypeFilterButton
            value={selectedTypes}
            options={typeOptions}
            onChange={setSelectedTypes}
            hideEmptyGroups={hideEmptyGroups}
            onHideEmptyGroupsChange={setHideEmptyGroups}
          />
        </div>
      </div>
      <ScrollArea className="flex-1 min-h-0">
        <DndContext
          sensors={sensors}
          onDragStart={handleDragStart}
          onDragMove={handleDragMove}
          onDragCancel={handleDragCancel}
          onDragEnd={handleDragEnd}
        >
          <div ref={treeContainerRef} className="p-2 isolate relative">
            <VirtualTreeRows
              rows={rows}
              groupById={groupById}
              assetById={assetById}
              ungroupedGroup={ungroupedGroup}
              totalCounts={treeIndex.totalCounts}
              pinnedRowKey={draggingRowKey}
              activeAssetIds={activeAssetIds}
              connectingAssetIds={connectingAssetIds}
              onSelectAsset={onSelectAsset}
              onAddAsset={onAddAsset}
              onEditAsset={onEditAsset}
              onCopyAsset={onCopyAsset}
              onConnectAsset={onConnectAsset}
              onConnectAssetInNewTab={onConnectAssetInNewTab}
              onOpenFileManager={onOpenFileManager}
              onDeleteAsset={setDeleteAssetConfirm}
              onMoveAsset={handleMoveAsset}
              onOpenInfoTab={onOpenInfoTab}
              onGroupContextMenu={handleGroupContextMenu}
              t={t}
            />
            {treeIsEmpty && (
              <div className="flex flex-col items-center gap-2 px-3 py-6 text-center">
                <div className="flex h-8 w-8 items-center justify-center rounded-md bg-muted text-muted-foreground">
                  <Server className="h-4 w-4" />
                </div>
                <div className="flex flex-col gap-1">
                  <p className="text-xs font-medium text-sidebar-foreground">
                    {t(isFilteredEmpty ? "asset.noMatchTitle" : "asset.emptyTitle")}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {t(isFilteredEmpty ? "asset.noMatchDesc" : "asset.emptyDesc")}
                  </p>
                </div>
                {isFilteredEmpty ? (
                  <div className="flex flex-wrap justify-center gap-1">
                    {hasSearchFilter && (
                      <Button variant="ghost" size="xs" onClick={() => setFilter("")}>
                        {t("asset.clearSearch")}
                      </Button>
                    )}
                    {hasTypeFilter && (
                      <Button variant="ghost" size="xs" onClick={() => setSelectedTypes([])}>
                        {t("asset.clearTypeFilter")}
                      </Button>
                    )}
                  </div>
                ) : (
                  <Button variant="outline" size="xs" onClick={() => onAddAsset()}>
                    <Plus className="h-3 w-3" />
                    {t("asset.addAsset")}
                  </Button>
                )}
              </div>
            )}
            <DropIndicator />
          </div>
          <DragOverlay dropAnimation={null}>
            {dragPreviewAsset ? (
              <AssetDragPreview asset={dragPreviewAsset} active={activeAssetIds.has(dragPreviewAsset.ID)} />
            ) : null}
          </DragOverlay>
        </DndContext>
      </ScrollArea>
      {groupContextMenu && (
        <ContextMenu open onOpenChange={(open) => !open && handleGroupContextMenuClose()}>
          <ContextMenuContent
            alignToStylePosition
            style={{
              position: "fixed",
              left: groupContextMenu.position.x,
              top: groupContextMenu.position.y,
            }}
            onContextMenu={(event) => event.preventDefault()}
          >
            {renderGroupContextMenu(groupContextMenu.group)}
          </ContextMenuContent>
        </ContextMenu>
      )}
      <AlertDialog open={!!deleteConfirm} onOpenChange={(open) => !open && setDeleteConfirm(null)}>
        <AlertDialogContent onOverlayClick={() => setDeleteConfirm(null)}>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("asset.deleteGroupTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteConfirm?.assetCount
                ? t("asset.deleteGroupDescDetailed", {
                    assetCount: deleteConfirm.assetCount,
                    childGroupCount: deleteConfirm.childGroupCount,
                  })
                : t("asset.deleteGroupDescChildrenOnly", { childGroupCount: deleteConfirm?.childGroupCount })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("action.cancel")}</AlertDialogCancel>
            {deleteConfirm?.assetCount ? (
              <>
                <AlertDialogAction onClick={() => handleConfirmDelete(false)}>
                  {t("asset.moveToUngrouped")}
                </AlertDialogAction>
                <AlertDialogAction variant="destructive" onClick={() => handleConfirmDelete(true)}>
                  {t("asset.deleteAssets")}
                </AlertDialogAction>
              </>
            ) : (
              <AlertDialogAction variant="destructive" onClick={() => handleConfirmDelete(false)}>
                {t("asset.deleteGroupOnly")}
              </AlertDialogAction>
            )}
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <ConfirmDialog
        open={!!deleteAssetConfirm}
        onOpenChange={(open) => !open && setDeleteAssetConfirm(null)}
        title={t("asset.deleteAssetTitle")}
        description={t("asset.deleteAssetDesc", { name: deleteAssetConfirm?.Name })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        confirmTestId="confirm-delete-asset"
        onConfirm={() => {
          if (deleteAssetConfirm) {
            deleteAsset(deleteAssetConfirm.ID);
          }
          setDeleteAssetConfirm(null);
        }}
      />
    </div>
  );
}

function GroupContextMenuContent({
  group,
  t,
  onAddAsset,
  onGroupDetail,
  onEditGroup,
  onMoveGroup,
  onDeleteGroup,
  onClose,
}: {
  group: group_entity.Group;
  t: (key: string) => string;
  onAddAsset: (groupId: number) => void;
  onGroupDetail: (group: group_entity.Group) => void;
  onEditGroup: (group: group_entity.Group) => void;
  onMoveGroup: (id: number, direction: string) => void;
  onDeleteGroup: (id: number) => void;
  onClose: () => void;
}) {
  const run = (action: () => void) => {
    action();
    onClose();
  };
  const isUngrouped = group.ID === 0;

  return (
    <>
      <ContextMenuItem onClick={() => run(() => onAddAsset(group.ID))}>
        <Plus className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.addAsset")}
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem disabled={isUngrouped} onClick={() => run(() => onGroupDetail(group))}>
        <Eye className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.groupDetailTitle")}
      </ContextMenuItem>
      <ContextMenuItem disabled={isUngrouped} onClick={() => run(() => onEditGroup(group))}>
        <Settings2 className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.editGroupSettings")}
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem disabled={isUngrouped} onClick={() => run(() => onMoveGroup(group.ID, "up"))}>
        <ArrowUp className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveUp")}
      </ContextMenuItem>
      <ContextMenuItem disabled={isUngrouped} onClick={() => run(() => onMoveGroup(group.ID, "down"))}>
        <ArrowDown className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveDown")}
      </ContextMenuItem>
      <ContextMenuItem disabled={isUngrouped} onClick={() => run(() => onMoveGroup(group.ID, "top"))}>
        <ChevronsUp className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveTop")}
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem
        disabled={isUngrouped}
        className="text-destructive"
        onClick={() => run(() => onDeleteGroup(group.ID))}
      >
        <Trash2 className="h-3.5 w-3.5 mr-1.5" />
        {t("action.delete")}
      </ContextMenuItem>
    </>
  );
}

function AssetDragPreview({ asset, active }: { asset: asset_entity.Asset; active: boolean }) {
  return (
    <div className="flex min-w-36 max-w-56 items-center gap-1.5 rounded-md border bg-popover px-2 py-1.5 text-sm shadow-lg">
      <EntityIcon
        icon={asset.Icon || undefined}
        fallback={Folder}
        className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
      />
      {active && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />}
      <span className="truncate text-popover-foreground">{asset.Name}</span>
    </div>
  );
}

// Fixed row heights (Tailwind line-height + vertical padding of each row kind), so the
// virtualizer positions rows without measuring the DOM.
const ROW_HEIGHTS: Record<Row["kind"], number> = { "group-header": 28, asset: 32, "empty-placeholder": 24 };
// Root-level groups keep the 2px gap the container's space-y-0.5 used to give them.
const ROOT_GROUP_GAP = 2;
const SCROLL_VIEWPORT_SELECTOR = '[data-slot="scroll-area-viewport"]';

function rowGap(row: Row, index: number): number {
  return row.kind === "group-header" && row.depth === 0 && index > 0 ? ROOT_GROUP_GAP : 0;
}

type RowCallbacks = {
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onAddAsset: (groupId: number) => void;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onCopyAsset: (asset: asset_entity.Asset) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onConnectAssetInNewTab?: (asset: asset_entity.Asset) => void;
  onOpenFileManager?: (asset: asset_entity.Asset) => void;
  onDeleteAsset: (asset: asset_entity.Asset) => void;
  onMoveAsset: (id: number, direction: string) => void;
  onOpenInfoTab?: (type: "asset" | "group", id: number, name: string, icon?: string) => void;
  onGroupContextMenu: (group: group_entity.Group, event: React.MouseEvent) => void;
  t: (key: string) => string;
};

type VirtualTreeRowsProps = RowCallbacks & {
  rows: Row[];
  groupById: Map<number, group_entity.Group>;
  assetById: Map<number, asset_entity.Asset>;
  ungroupedGroup: group_entity.Group;
  totalCounts: Map<number, number>;
  pinnedRowKey: string | null;
  activeAssetIds: Set<number>;
  connectingAssetIds: Set<number>;
};

// Renders the flattened tree (the same `rows` the DnD logic uses) through a virtualizer:
// only rows near the viewport are mounted. Memoized so scrolling re-renders this list
// alone, and each row is memoized so a re-render here only touches rows whose props changed.
const VirtualTreeRows = React.memo(function VirtualTreeRows({
  rows,
  groupById,
  assetById,
  ungroupedGroup,
  totalCounts,
  pinnedRowKey,
  activeAssetIds,
  connectingAssetIds,
  onSelectAsset,
  onAddAsset,
  onEditAsset,
  onCopyAsset,
  onConnectAsset,
  onConnectAssetInNewTab,
  onOpenFileManager,
  onDeleteAsset,
  onMoveAsset,
  onOpenInfoTab,
  onGroupContextMenu,
  t,
}: VirtualTreeRowsProps) {
  const listRef = useRef<HTMLDivElement>(null);
  const pinnedIndex = useMemo(
    () => (pinnedRowKey === null ? -1 : rows.findIndex((row) => rowKey(row) === pinnedRowKey)),
    [rows, pinnedRowKey]
  );
  const rangeExtractor = useCallback(
    (range: Range) => {
      const indexes = defaultRangeExtractor(range);
      if (pinnedIndex < 0 || indexes.includes(pinnedIndex)) return indexes;
      return [...indexes, pinnedIndex].sort((a, b) => a - b);
    },
    [pinnedIndex]
  );
  // getItemKey changes with rows, which makes the virtualizer recompute row offsets.
  const getItemKey = useCallback((index: number) => rowKey(rows[index]), [rows]);
  const estimateSize = useCallback(
    (index: number) => ROW_HEIGHTS[rows[index].kind] + rowGap(rows[index], index),
    [rows]
  );
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => listRef.current?.closest<HTMLElement>(SCROLL_VIEWPORT_SELECTOR) ?? null,
    estimateSize,
    getItemKey,
    rangeExtractor,
    overscan: 10,
  });

  return (
    <div ref={listRef} className="relative" style={{ height: virtualizer.getTotalSize() }}>
      {virtualizer.getVirtualItems().map((item) => {
        const row = rows[item.index];
        let content: React.ReactNode;
        if (row.kind === "group-header") {
          content = (
            <GroupHeaderRow
              group={row.groupID === 0 ? ungroupedGroup : groupById.get(row.groupID)!}
              depth={row.depth}
              collapsed={row.collapsed}
              totalCount={totalCounts.get(row.groupID)!}
              onGroupContextMenu={onGroupContextMenu}
            />
          );
        } else if (row.kind === "asset") {
          content = (
            <AssetRow
              asset={assetById.get(row.assetID)!}
              depth={row.depth}
              active={activeAssetIds.has(row.assetID)}
              connecting={connectingAssetIds.has(row.assetID)}
              onSelectAsset={onSelectAsset}
              onEditAsset={onEditAsset}
              onCopyAsset={onCopyAsset}
              onConnectAsset={onConnectAsset}
              onConnectAssetInNewTab={onConnectAssetInNewTab}
              onOpenFileManager={onOpenFileManager}
              onDeleteAsset={onDeleteAsset}
              onMoveAsset={onMoveAsset}
              onOpenInfoTab={onOpenInfoTab}
              t={t}
            />
          );
        } else {
          content = <EmptyGroupRow groupID={row.groupID} depth={row.depth} onAddAsset={onAddAsset} t={t} />;
        }
        return (
          <div
            key={item.key}
            className="absolute left-0 w-full"
            style={{ top: item.start, height: item.size, paddingTop: rowGap(row, item.index) }}
          >
            {content}
          </div>
        );
      })}
    </div>
  );
});

const GroupHeaderRow = React.memo(function GroupHeaderRow({
  group,
  depth,
  collapsed,
  totalCount,
  onGroupContextMenu,
}: {
  group: group_entity.Group;
  depth: number;
  collapsed: boolean;
  totalCount: number;
  onGroupContextMenu: (group: group_entity.Group, event: React.MouseEvent) => void;
}) {
  /* eslint-disable react-hooks/refs */
  const toggleGroupCollapsed = useAssetStore((s) => s.toggleGroupCollapsed);
  const isDndHighlighted = useAssetTreeDndStore((s) => s.highlightedGroupID === group.ID);
  const isUngrouped = group.ID === 0;

  const sortable = useSortable({
    id: `group-${group.ID}`,
    disabled: isUngrouped ? { draggable: true, droppable: false } : false,
  });
  const groupRowStyle: React.CSSProperties = {
    paddingLeft: `${8 + depth * 12}px`,
    opacity: sortable.isDragging ? 0.5 : undefined,
    position: "relative",
    zIndex: sortable.isDragging ? 20 : undefined,
  };

  const groupActivatorProps = !isUngrouped
    ? {
        ...sortable.attributes,
        ...sortable.listeners,
      }
    : {};

  return (
    <div
      // dnd-kit's setNodeRef is a callback, not a React ref.
      ref={sortable.setNodeRef}
      data-asset-tree-row={`group-${group.ID}`}
      className={`flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring/60 cursor-pointer transition-colors duration-150 ${
        isDndHighlighted ? "bg-primary/10 ring-1 ring-primary/40" : "hover:bg-sidebar-accent"
      }`}
      style={groupRowStyle}
      onClick={() => toggleGroupCollapsed(group.ID)}
      onContextMenu={(e) => onGroupContextMenu(group, e)}
      {...groupActivatorProps}
    >
      {collapsed ? (
        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      ) : (
        <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      )}
      <EntityIcon icon={group.Icon} fallback={Folder} className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="truncate text-sidebar-foreground">{group.Name}</span>
      <span className="ml-auto text-xs text-muted-foreground">{totalCount}</span>
    </div>
  );
});

const EmptyGroupRow = React.memo(function EmptyGroupRow({
  groupID,
  depth,
  onAddAsset,
  t,
}: {
  groupID: number;
  depth: number;
  onAddAsset: (groupId: number) => void;
  t: (key: string) => string;
}) {
  const isDndHighlighted = useAssetTreeDndStore((s) => s.highlightedGroupID === groupID);
  return (
    <div
      data-asset-tree-row={`empty-${groupID}`}
      className={`pr-2 py-1 text-xs cursor-pointer rounded-md transition-colors duration-150 ${
        isDndHighlighted
          ? "bg-primary/10 ring-1 ring-primary/40 text-foreground"
          : "text-muted-foreground hover:underline"
      }`}
      style={{ paddingLeft: `${20 + depth * 12}px` }}
      onClick={() => onAddAsset(groupID)}
    >
      + {t("asset.addAsset")}
    </div>
  );
});

type AssetRowProps = {
  asset: asset_entity.Asset;
  depth: number;
  active: boolean;
  connecting: boolean;
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onCopyAsset: (asset: asset_entity.Asset) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onConnectAssetInNewTab?: (asset: asset_entity.Asset) => void;
  onOpenFileManager?: (asset: asset_entity.Asset) => void;
  onDeleteAsset: (asset: asset_entity.Asset) => void;
  onMoveAsset: (id: number, direction: string) => void;
  onOpenInfoTab?: (type: "asset" | "group", id: number, name: string, icon?: string) => void;
  t: (key: string) => string;
};

// Thin wrapper: only this component subscribes to DndContext. It re-renders on every
// drag move, but its body is just a div + children passthrough, and `children` is the
// same React element reference as long as AssetRowContent's props don't change, so
// React's reconciler skips the heavy subtree.
function AssetRowDraggable({ id, children }: { id: string; children: React.ReactNode }) {
  const { setNodeRef, attributes, listeners, isDragging } = useDraggable({ id });
  return (
    <div
      ref={setNodeRef}
      data-asset-tree-row={id}
      className="relative"
      style={{
        zIndex: isDragging ? 20 : undefined,
        opacity: isDragging ? 0.5 : undefined,
      }}
      {...attributes}
      {...listeners}
    >
      {children}
    </div>
  );
}

function CopyAssetRefMenuItem({ asset, t }: { asset: asset_entity.Asset; t: (key: string) => string }) {
  const binding = useShortcutStore((s) => s.shortcuts["asset.copyRef"]);
  return (
    <ContextMenuItem
      data-testid="asset-context-copy-ref"
      onClick={() => {
        void writeAssetMarkdownRef(asset.Name, asset.ID)
          .then(() => notifyCopied(t("asset.copyRefCopied")))
          .catch((err) => toast.error(errorMessage(err)));
      }}
    >
      <Link2 className="h-3.5 w-3.5 mr-1.5" />
      {t("asset.copyRef")}
      <ContextMenuShortcut>{formatBinding(binding)}</ContextMenuShortcut>
    </ContextMenuItem>
  );
}

const AssetRow = React.memo(function AssetRow(props: AssetRowProps) {
  return (
    <AssetRowDraggable id={`asset-${props.asset.ID}`}>
      <AssetRowContent {...props} />
    </AssetRowDraggable>
  );
});

const AssetRowContent = React.memo(function AssetRowContent({
  asset,
  depth,
  active,
  connecting: isConnecting,
  onSelectAsset,
  onEditAsset,
  onCopyAsset,
  onConnectAsset,
  onConnectAssetInNewTab,
  onOpenFileManager,
  onDeleteAsset,
  onMoveAsset,
  onOpenInfoTab,
  t,
}: AssetRowProps) {
  // Per-row selector: a selection change re-renders only the rows whose state flips.
  const selected = useAssetStore((s) => s.selectedAssetId === asset.ID);
  const style: React.CSSProperties = {
    paddingLeft: `${20 + depth * 12}px`,
  };

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          className={`flex items-center gap-1.5 rounded-md pr-2 py-1.5 text-sm outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring/60 cursor-pointer select-none transition-colors duration-150 ${
            selected ? "bg-sidebar-accent text-sidebar-accent-foreground" : "hover:bg-sidebar-accent"
          }`}
          style={style}
          onClick={() => onSelectAsset(asset)}
          onDoubleClick={() => {
            onSelectAsset(asset);
            const def = getAssetType(asset.Type);
            if (def?.canConnect && (def.connectAction === "query" || !isConnecting)) {
              onConnectAsset(asset);
            }
          }}
        >
          {isConnecting ? (
            <Loader2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground animate-spin" />
          ) : (
            <EntityIcon
              icon={asset.Icon || undefined}
              fallback={Server}
              className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
            />
          )}
          {active && <span className="h-1.5 w-1.5 rounded-full bg-success shrink-0" />}
          <span className="truncate text-sidebar-foreground">{asset.Name}</span>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        {getAssetType(asset.Type)?.canConnect && (
          <ContextMenuItem onClick={() => onConnectAsset(asset)} disabled={isConnecting}>
            {isConnecting ? (
              <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
            ) : (
              <TerminalSquare className="h-3.5 w-3.5 mr-1.5" />
            )}
            {t("asset.connect")}
          </ContextMenuItem>
        )}
        {getAssetType(asset.Type)?.canConnectInNewTab && onConnectAssetInNewTab && (
          <ContextMenuItem onClick={() => onConnectAssetInNewTab(asset)} disabled={isConnecting}>
            <ExternalLink className="h-3.5 w-3.5 mr-1.5" />
            {t("asset.connectInNewTab")}
          </ContextMenuItem>
        )}
        {getAssetType(asset.Type)?.canOpenFileManager && onOpenFileManager && (
          <ContextMenuItem onClick={() => onOpenFileManager(asset)}>
            <FolderOpen className="h-3.5 w-3.5 mr-1.5" />
            {t("sftp.fileManager")}
          </ContextMenuItem>
        )}
        {onOpenInfoTab && (
          <ContextMenuItem
            data-testid="asset-context-detail"
            onClick={() => onOpenInfoTab("asset", asset.ID, asset.Name, asset.Icon)}
          >
            <Eye className="h-3.5 w-3.5 mr-1.5" />
            {t("action.editPermission")}
          </ContextMenuItem>
        )}
        <ContextMenuItem data-testid="asset-context-edit" onClick={() => onEditAsset(asset)}>
          <Pencil className="h-3.5 w-3.5 mr-1.5" />
          {t("action.edit")}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => onCopyAsset(asset)}>
          <Copy className="h-3.5 w-3.5 mr-1.5" />
          {t("action.copy")}
        </ContextMenuItem>
        <CopyAssetRefMenuItem asset={asset} t={t} />
        <ContextMenuSeparator />
        <ContextMenuItem onClick={() => onMoveAsset(asset.ID, "up")}>
          <ArrowUp className="h-3.5 w-3.5 mr-1.5" />
          {t("asset.moveUp")}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => onMoveAsset(asset.ID, "down")}>
          <ArrowDown className="h-3.5 w-3.5 mr-1.5" />
          {t("asset.moveDown")}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => onMoveAsset(asset.ID, "top")}>
          <ChevronsUp className="h-3.5 w-3.5 mr-1.5" />
          {t("asset.moveTop")}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem
          data-testid="asset-context-delete"
          className="text-destructive"
          onClick={() => onDeleteAsset(asset)}
        >
          <Trash2 className="h-3.5 w-3.5 mr-1.5" />
          {t("action.delete")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
});
