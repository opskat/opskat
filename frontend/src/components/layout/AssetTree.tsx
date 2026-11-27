import React, { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useFullscreen } from "@/hooks/useFullscreen";
import {
  ChevronRight,
  ChevronDown,
  Folder,
  FolderOpen,
  FolderInput,
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
  hoverExpandGroupID,
  insertionToReorderArgs,
  rowKey,
  useAssetTreeDndStore,
  type DragActive,
  type InsertionPoint,
  type Row,
  type RowRect,
} from "@/lib/assetTreeDnd";
import { reorderAssetsBatchOptimistically } from "@/lib/assetTreeReorderOptimistic";
import { copyAssetMarkdownRefs, writeAssetMarkdownRef } from "@/lib/assetRef";
import { notifyCopied, notifySuccess } from "@/lib/notify";
import { getAssetType } from "@/lib/assetTypes";
import { useAssetTypeOptions, matchSelectedTypes } from "@/lib/assetTypes/options";
import { AssetTypeFilterButton } from "@/components/asset/AssetTypeFilterButton";
import { selectLoadError, useAssetStore } from "@/stores/assetStore";
import { eventMatchesBinding, formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useActiveAssetIds } from "@/hooks/useActiveAssetIds";
import { MoveAsset, MoveGroup, ReorderAssets, ReorderGroup } from "../../../wailsjs/go/system/System";
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
import { AsyncState } from "@/components/common/AsyncState";
import {
  isMorePermissiveMode,
  resolveEffectivePermissionMode,
  type EffectivePermissionMode,
} from "@/lib/commandReview";
import { AssetSelectionBar } from "./AssetSelectionBar";
import { MoveAssetsDialog } from "./MoveAssetsDialog";

interface AssetTreeProps {
  collapsed: boolean;
  sidebarHidden?: boolean;
  onShowSidebar?: () => void;
  onAddAsset: (groupId?: number) => void;
  onAddGroup: (parentId?: number) => void;
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

type TreeContextMenuState =
  | { kind: "group"; group: group_entity.Group; position: { x: number; y: number } }
  | { kind: "asset"; asset: asset_entity.Asset; position: { x: number; y: number } }
  | {
      kind: "assets-batch";
      selectedCount: number;
      connectableCount: number;
      position: { x: number; y: number };
    }
  | { kind: "blank"; position: { x: number; y: number } };

type DragPreviewState =
  { kind: "asset"; asset: asset_entity.Asset; count: number } | { kind: "group"; group: group_entity.Group };

const FILTER_LS_KEY = "asset_tree_type_filter";
const HIDE_EMPTY_LS_KEY = "asset_tree_hide_empty_groups";
const BATCH_CONNECT_CONFIRM_THRESHOLD = 5;

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
// sidebar the focused surface, which is what grants the asset-reference & selection shortcuts.
const SIDEBAR_INTERACTIVE_SELECTOR = "input, textarea, select, button, a[href], [contenteditable]";

function focusSidebarUnlessInteractive(e: React.MouseEvent<HTMLDivElement>) {
  const target = e.target as HTMLElement | null;
  if (!target || !e.currentTarget.contains(target)) return;
  if (target.closest(SIDEBAR_INTERACTIVE_SELECTOR)) return;
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
  if (point.kind === "after-asset" || point.kind === "after-group") {
    const target =
      point.kind === "after-asset"
        ? rowsList.find((r) => r.kind === "asset" && r.assetID === point.assetID)
        : rowsList.find((r) => r.kind === "group-header" && r.groupID === point.groupID);
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

function UngroupedDropZone({ zoneRef, label }: { zoneRef: React.RefObject<HTMLDivElement | null>; label: string }) {
  const isHighlighted = useAssetTreeDndStore((s) => s.highlightedGroupID === 0);
  return (
    <div
      ref={zoneRef}
      data-testid="asset-tree-ungrouped-drop-zone"
      className={`flex items-center justify-center gap-1.5 rounded-md border border-dashed px-2 py-2 text-xs transition-colors duration-150 ${
        isHighlighted
          ? "border-primary bg-primary/10 text-primary font-medium"
          : "border-sidebar-border text-muted-foreground"
      }`}
    >
      <Folder className="h-3.5 w-3.5 shrink-0" />
      <span className="truncate">{label}</span>
    </div>
  );
}

function parseActive(id: string): { kind: "asset" | "group"; id: number } | null {
  const m = /^(asset|group)-(\d+)$/.exec(id);
  if (!m) return null;
  return { kind: m[1] as "asset" | "group", id: Number(m[2]) };
}

function findElevatedPermissionMode(
  movedAssets: asset_entity.Asset[],
  targetGroupID: number,
  groups: group_entity.Group[]
): { count: number; mode: EffectivePermissionMode } | null {
  let count = 0;
  let maxMode: EffectivePermissionMode = "default";
  for (const asset of movedAssets) {
    if ((asset.GroupID || 0) === targetGroupID) continue;
    const before = resolveEffectivePermissionMode(asset, groups);
    const after = resolveEffectivePermissionMode({ ...asset, GroupID: targetGroupID }, groups);
    if (isMorePermissiveMode(before, after)) {
      count++;
      if (isMorePermissiveMode(maxMode, after)) maxMode = after;
    }
  }
  return count > 0 ? { count, mode: maxMode } : null;
}

function isOrderUnchanged(
  assets: asset_entity.Asset[],
  movedIDs: number[],
  targetGroupID: number,
  beforeID: number
): boolean {
  const byID = new Map(assets.map((a) => [a.ID, a] as const));
  for (const id of movedIDs) {
    const a = byID.get(id);
    if (!a || (a.GroupID || 0) !== targetGroupID) return false;
  }
  const beforeOrder = assets.filter((a) => (a.GroupID || 0) === targetGroupID).map((a) => a.ID);
  const afterAssets = reorderAssetsBatchOptimistically(assets, movedIDs, targetGroupID, beforeID);
  const afterOrder = afterAssets.filter((a) => (a.GroupID || 0) === targetGroupID).map((a) => a.ID);
  if (beforeOrder.length !== afterOrder.length) return false;
  return beforeOrder.every((id, idx) => id === afterOrder[idx]);
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
  const deleteAsset = useAssetStore((s) => s.deleteAsset);
  const deleteAssets = useAssetStore((s) => s.deleteAssets);
  const deleteGroup = useAssetStore((s) => s.deleteGroup);
  const refresh = useAssetStore((s) => s.refresh);
  const initialized = useAssetStore((s) => s.initialized);
  const loadError = useAssetStore(selectLoadError);
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
  const [batchDeleteConfirm, setBatchDeleteConfirm] = useState<asset_entity.Asset[] | null>(null);
  const [batchConnectConfirm, setBatchConnectConfirm] = useState<asset_entity.Asset[] | null>(null);
  const [moveAssetsTarget, setMoveAssetsTarget] = useState<asset_entity.Asset[] | null>(null);
  const [contextMenu, setContextMenu] = useState<TreeContextMenuState | null>(null);
  const [dragPreview, setDragPreview] = useState<DragPreviewState | null>(null);
  const [showUngroupedDropZone, setShowUngroupedDropZone] = useState(false);
  // Row being dragged: kept mounted by the virtualizer even when scrolled out of view.
  const [draggingRowKey, setDraggingRowKey] = useState<string | null>(null);

  useEffect(() => {
    // Failures are recorded in the store (`loadError`) and rendered with a retry below.
    refresh().catch(() => {});
  }, [refresh]);

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
  const isFiltered = hasSearchFilter || hasTypeFilter;
  const shouldHideEmptyGroups = hideEmptyGroups || isFiltered;

  const treeIndex = useMemo(() => buildTreeIndex(groups, filteredAssets), [groups, filteredAssets]);
  const groupById = useMemo(() => new Map(groups.map((g) => [g.ID, g] as const)), [groups]);
  const assetById = useMemo(() => new Map(filteredAssets.map((a) => [a.ID, a] as const)), [filteredAssets]);
  const ungroupedGroup = useMemo(() => new group_entity.Group({ ID: 0, Name: t("asset.ungrouped") }), [t]);

  const isFilteredEmpty = filteredAssets.length === 0 && isFiltered;

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
  const visibleAssetIdsRef = useRef<number[]>([]);
  const draggingAssetIdsRef = useRef<number[]>([]);
  const treeContainerRef = useRef<HTMLDivElement>(null);
  const ungroupedDropZoneRef = useRef<HTMLDivElement>(null);

  const rows = useMemo(
    () =>
      flattenTree({
        index: treeIndex,
        collapsedGroupIDs: new Set(collapsedGroupIds),
        shouldHideEmpty: shouldHideEmptyGroups,
      }),
    [treeIndex, collapsedGroupIds, shouldHideEmptyGroups]
  );
  const visibleAssetIds = useMemo(
    () => rows.filter((r): r is Extract<Row, { kind: "asset" }> => r.kind === "asset").map((r) => r.assetID),
    [rows]
  );

  useEffect(() => {
    rowsRef.current = rows;
    visibleAssetIdsRef.current = visibleAssetIds;
  }, [rows, visibleAssetIds]);

  // 折叠分组或更改搜索/类型筛选时自动剔除不可见的已选资产，避免“幽灵误删/误拖”。
  useEffect(() => {
    if (!initialized) return;
    useAssetStore.getState().pruneSelection(new Set(visibleAssetIds));
  }, [initialized, visibleAssetIds]);

  const treeIsEmpty = rows.length === 0;

  const getSelectedVisibleAssets = useCallback((): asset_entity.Asset[] => {
    const { selectedAssetId, selectedAssetIds, selectedAssetIdSet } = useAssetStore.getState();
    const effectiveSet =
      selectedAssetIds.length > 0 ? selectedAssetIdSet : selectedAssetId != null ? new Set([selectedAssetId]) : null;
    if (!effectiveSet || effectiveSet.size === 0) return [];
    const out: asset_entity.Asset[] = [];
    for (const id of visibleAssetIdsRef.current) {
      if (!effectiveSet.has(id)) continue;
      const asset = assetById.get(id);
      if (asset) out.push(asset);
    }
    return out;
  }, [assetById]);

  const notifyPermissionElevation = useCallback(
    (movedAssets: asset_entity.Asset[], targetGroupID: number) => {
      const elevated = findElevatedPermissionMode(movedAssets, targetGroupID, groups);
      if (!elevated) return;
      const targetName = targetGroupID === 0 ? t("asset.ungrouped") : (groupById.get(targetGroupID)?.Name ?? "");
      toast.warning(
        t("asset.permissionModeElevated", {
          group: targetName,
          mode: t(`commandReview.mode.${elevated.mode}`),
          count: elevated.count,
        })
      );
    },
    [groups, groupById, t]
  );

  const handleAssetRowClick = useCallback(
    (asset: asset_entity.Asset, event: React.MouseEvent) => {
      event.stopPropagation();
      const store = useAssetStore.getState();
      if (event.shiftKey) {
        store.selectAssetRange(visibleAssetIdsRef.current, asset.ID);
        return;
      }
      if (event.ctrlKey || event.metaKey) {
        store.toggleAssetSelection(asset.ID);
        return;
      }
      onSelectAsset(asset);
    },
    [onSelectAsset]
  );

  const runBatchConnect = useCallback(
    async (list: asset_entity.Asset[]) => {
      for (const asset of list) {
        await onConnectAsset(asset);
      }
    },
    [onConnectAsset]
  );

  const handleRequestBatchConnect = useCallback(() => {
    const connectable = getSelectedVisibleAssets().filter((a) => getAssetType(a.Type)?.canConnect);
    if (connectable.length === 0) return;
    if (connectable.length > BATCH_CONNECT_CONFIRM_THRESHOLD) {
      setBatchConnectConfirm(connectable);
      return;
    }
    void runBatchConnect(connectable);
  }, [getSelectedVisibleAssets, runBatchConnect]);

  const handleRequestBatchMove = useCallback(() => {
    const selected = getSelectedVisibleAssets();
    if (selected.length === 0) return;
    setMoveAssetsTarget(selected);
  }, [getSelectedVisibleAssets]);

  const handleRequestBatchCopyRefs = useCallback(() => {
    const selected = getSelectedVisibleAssets();
    if (selected.length === 0) return;
    void copyAssetMarkdownRefs(selected).catch((err) => toast.error(errorMessage(err)));
  }, [getSelectedVisibleAssets]);

  const handleRequestBatchDelete = useCallback(() => {
    const selected = getSelectedVisibleAssets();
    if (selected.length === 0) return;
    if (selected.length === 1) {
      setDeleteAssetConfirm(selected[0]);
      return;
    }
    setBatchDeleteConfirm(selected);
  }, [getSelectedVisibleAssets]);

  const handleClearSelection = useCallback(() => {
    useAssetStore.getState().clearSelection();
  }, []);

  const handleConfirmMoveAssets = useCallback(
    async (targetGroupID: number): Promise<boolean> => {
      if (!moveAssetsTarget || moveAssetsTarget.length === 0) return false;
      const moved = moveAssetsTarget;
      const ids = moved.map((a) => a.ID);
      try {
        await ReorderAssets(ids, targetGroupID, 0);
        if (targetGroupID > 0) useAssetStore.getState().expandGroup(targetGroupID);
        await refresh();
        notifySuccess(t("asset.movedAssetsSuccess", { count: ids.length }));
        notifyPermissionElevation(moved, targetGroupID);
        return true;
      } catch (e) {
        toast.error(errorMessage(e));
        return false;
      }
    },
    [moveAssetsTarget, refresh, notifyPermissionElevation, t]
  );

  const handleSidebarKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (e.nativeEvent.isComposing) return;
      const target = e.target as HTMLElement | null;
      if (!target || !e.currentTarget.contains(target)) return;
      if (target.closest(SIDEBAR_INTERACTIVE_SELECTOR)) return;
      if (
        contextMenu ||
        deleteConfirm ||
        deleteAssetConfirm ||
        batchDeleteConfirm ||
        batchConnectConfirm ||
        moveAssetsTarget
      ) {
        return;
      }

      if (e.key === "Escape") {
        const { selectedAssetIds, selectedAssetId } = useAssetStore.getState();
        if (selectedAssetIds.length > 0 || selectedAssetId != null) {
          e.preventDefault();
          useAssetStore.getState().clearSelection();
        }
        return;
      }

      const { shortcuts } = useShortcutStore.getState();
      if (eventMatchesBinding(e.nativeEvent, shortcuts["asset.selectAll"])) {
        e.preventDefault();
        e.stopPropagation();
        useAssetStore.getState().selectAllAssets(visibleAssetIdsRef.current);
        return;
      }

      if (eventMatchesBinding(e.nativeEvent, shortcuts["asset.delete"])) {
        if (getSelectedVisibleAssets().length === 0) return;
        e.preventDefault();
        e.stopPropagation();
        handleRequestBatchDelete();
      }
    },
    [
      contextMenu,
      deleteConfirm,
      deleteAssetConfirm,
      batchDeleteConfirm,
      batchConnectConfirm,
      moveAssetsTarget,
      getSelectedVisibleAssets,
      handleRequestBatchDelete,
    ]
  );

  const handleBlankAreaClick = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const target = e.target as HTMLElement | null;
    if (!target || !e.currentTarget.contains(target)) return;
    if (target.closest("[data-asset-tree-row]") || target.closest(SIDEBAR_INTERACTIVE_SELECTOR)) return;
    useAssetStore.getState().clearSelection();
  }, []);

  const handleBlankContextMenu = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const target = e.target as HTMLElement | null;
    if (!target || !e.currentTarget.contains(target)) return;
    if (target.closest("[data-asset-tree-row]") || target.closest(SIDEBAR_INTERACTIVE_SELECTOR)) return;
    e.preventDefault();
    setContextMenu({ kind: "blank", position: { x: e.clientX, y: e.clientY } });
  }, []);

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

  const resetDragState = useCallback(() => {
    draggingAssetIdsRef.current = [];
    setDragPreview(null);
    setShowUngroupedDropZone(false);
    setDraggingRowKey(null);
    useAssetTreeDndStore.getState().clear();
    clearHoverExpand();
  }, [clearHoverExpand]);

  const handleDragStart = (e: DragStartEvent) => {
    setDraggingRowKey(String(e.active.id));
    const active = parseActive(String(e.active.id));
    if (active?.kind === "asset") {
      const { selectedAssetIds, selectedAssetIdSet } = useAssetStore.getState();
      const dragIDs =
        selectedAssetIdSet.has(active.id) && selectedAssetIds.length > 1
          ? visibleAssetIdsRef.current.filter((id) => selectedAssetIdSet.has(id))
          : [active.id];
      const finalIDs = dragIDs.length > 0 ? dragIDs : [active.id];
      draggingAssetIdsRef.current = finalIDs;
      useAssetTreeDndStore.getState().setDraggingAssetIDs(finalIDs);

      const leadAsset = assets.find((asset) => asset.ID === active.id) ?? null;
      setDragPreview(leadAsset ? { kind: "asset", asset: leadAsset, count: finalIDs.length } : null);

      const hasUngroupedBucket = (treeIndex.assetsByGroup.get(0)?.length ?? 0) > 0;
      const hasGroupedInDrag = finalIDs.some((id) => (assets.find((a) => a.ID === id)?.GroupID || 0) !== 0);
      setShowUngroupedDropZone(!hasUngroupedBucket && hasGroupedInDrag);
    } else if (active?.kind === "group") {
      draggingAssetIdsRef.current = [];
      useAssetTreeDndStore.getState().setDraggingAssetIDs([]);
      const group = groupById.get(active.id) ?? null;
      setDragPreview(group ? { kind: "group", group } : null);
      setShowUngroupedDropZone(false);
    } else {
      resetDragState();
    }
  };

  const handleDragMove = (e: DragMoveEvent) => {
    const parsed = parseActive(String(e.active.id));
    if (!parsed) return;
    const active: DragActive =
      parsed.kind === "asset" ? { kind: "asset", id: parsed.id, ids: draggingAssetIdsRef.current } : parsed;

    const activatorEvent = e.activatorEvent as PointerEvent;
    const pointerY = activatorEvent.clientY + e.delta.y;
    const rects = collectRowRects();
    const ungroupedEl = ungroupedDropZoneRef.current;
    const ungroupedRect = ungroupedEl ? ungroupedEl.getBoundingClientRect() : null;
    const ungroupedZoneRect: RowRect | null = ungroupedRect
      ? { top: ungroupedRect.top, bottom: ungroupedRect.bottom, height: ungroupedRect.height }
      : null;

    const point = computeInsertionPoint({
      rows: rowsRef.current,
      rowRects: rects,
      pointerY,
      active,
      groups,
      isFiltered,
      ungroupedZoneRect,
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
    const hoverGroupID = hoverExpandGroupID(point);
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
    resetDragState();
  };

  const handleDragEnd = async (e: DragEndEvent) => {
    const draggedIDs = draggingAssetIdsRef.current;
    const point = useAssetTreeDndStore.getState().point;
    resetDragState();
    if (!point) return;

    const parsed = parseActive(String(e.active.id));
    if (!parsed) return;
    const active: DragActive = parsed.kind === "asset" ? { kind: "asset", id: parsed.id, ids: draggedIDs } : parsed;

    const args = insertionToReorderArgs({ point, active, groups, assets, isFiltered });
    if (!args) return;

    let appliedOptimistic = false;
    let prevAssets: asset_entity.Asset[] | null = null;
    try {
      if (args.kind === "asset") {
        if (
          isFiltered &&
          args.ids.every((id) => {
            const a = assetById.get(id);
            return a != null && (a.GroupID || 0) === args.targetGroupID;
          })
        ) {
          return;
        }
        if (isOrderUnchanged(assets, args.ids, args.targetGroupID, args.beforeID)) return;
        const movedSnapshots = args.ids
          .map((id) => assets.find((a) => a.ID === id))
          .filter((a): a is asset_entity.Asset => a != null);
        await afterDragCleanupFrame();
        prevAssets = useAssetStore.getState().assets;
        useAssetStore.setState((state) => ({
          assets: reorderAssetsBatchOptimistically(state.assets, args.ids, args.targetGroupID, args.beforeID),
        }));
        appliedOptimistic = true;
        await ReorderAssets(args.ids, args.targetGroupID, args.beforeID);
        appliedOptimistic = false;
        await refresh();
        notifyPermissionElevation(movedSnapshots, args.targetGroupID);
      } else {
        await ReorderGroup(args.id, args.targetParentID, args.beforeID);
        await refresh();
      }
    } catch (err) {
      if (appliedOptimistic && prevAssets) {
        useAssetStore.setState({ assets: prevAssets });
        void refresh().catch(() => {});
      }
      toast.error(errorMessage(err));
    }
  };

  const handleConfirmDelete = async (deleteAssetsInGroup: boolean) => {
    if (!deleteConfirm) return;
    try {
      await deleteGroup(deleteConfirm.id, deleteAssetsInGroup);
    } catch (e) {
      toast.error(errorMessage(e));
    }
    setDeleteConfirm(null);
  };

  const handleContextMenuClose = useCallback(() => setContextMenu(null), []);

  const handleGroupContextMenu = useCallback((group: group_entity.Group, event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    setContextMenu({ kind: "group", group, position: { x: event.clientX, y: event.clientY } });
  }, []);

  const handleAssetContextMenu = useCallback(
    (asset: asset_entity.Asset, event: React.MouseEvent) => {
      event.preventDefault();
      event.stopPropagation();
      const { selectedAssetIds, selectedAssetIdSet } = useAssetStore.getState();
      if (selectedAssetIdSet.has(asset.ID) && selectedAssetIds.length > 1) {
        const selected = getSelectedVisibleAssets();
        const connectableCount = selected.filter((a) => getAssetType(a.Type)?.canConnect).length;
        setContextMenu({
          kind: "assets-batch",
          selectedCount: selected.length,
          connectableCount,
          position: { x: event.clientX, y: event.clientY },
        });
        return;
      }
      onSelectAsset(asset);
      setContextMenu({ kind: "asset", asset, position: { x: event.clientX, y: event.clientY } });
    },
    [getSelectedVisibleAssets, onSelectAsset]
  );

  if (collapsed) return null;

  return (
    <div
      data-testid="asset-tree"
      data-asset-sidebar=""
      tabIndex={-1}
      onMouseDown={focusSidebarUnlessInteractive}
      onKeyDown={handleSidebarKeyDown}
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
                  onClick={() => onAddGroup(0)}
                  aria-label={t("asset.addGroup")}
                  data-testid="add-group-button"
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
              data-testid="asset-tree-search"
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
      <AssetSelectionBar
        onConnectSelected={handleRequestBatchConnect}
        onMoveSelected={handleRequestBatchMove}
        onCopyRefsSelected={handleRequestBatchCopyRefs}
        onDeleteSelected={handleRequestBatchDelete}
        onClearSelection={handleClearSelection}
      />
      <DndContext
        sensors={sensors}
        onDragStart={handleDragStart}
        onDragMove={handleDragMove}
        onDragCancel={handleDragCancel}
        onDragEnd={handleDragEnd}
      >
        <ScrollArea className="flex-1 min-h-0" onClick={handleBlankAreaClick} onContextMenu={handleBlankContextMenu}>
          <div ref={treeContainerRef} className="p-2 isolate relative min-h-full">
            <VirtualTreeRows
              rows={rows}
              groupById={groupById}
              assetById={assetById}
              ungroupedGroup={ungroupedGroup}
              totalCounts={treeIndex.totalCounts}
              pinnedRowKey={draggingRowKey}
              activeAssetIds={activeAssetIds}
              connectingAssetIds={connectingAssetIds}
              onAssetClick={handleAssetRowClick}
              onSelectAsset={onSelectAsset}
              onAddAsset={onAddAsset}
              onConnectAsset={onConnectAsset}
              onGroupContextMenu={handleGroupContextMenu}
              onAssetContextMenu={handleAssetContextMenu}
              t={t}
            />
            <AsyncState
              status={loadError ? "error" : initialized ? "ready" : "loading"}
              error={loadError}
              onRetry={() => refresh().catch(() => {})}
              className="py-6"
              empty={
                treeIsEmpty ? (
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
                ) : undefined
              }
            />
            <DropIndicator />
          </div>
        </ScrollArea>
        {showUngroupedDropZone && (
          <div className="shrink-0 border-t border-panel-divider px-2 py-1.5">
            <UngroupedDropZone zoneRef={ungroupedDropZoneRef} label={t("asset.dropToUngrouped")} />
          </div>
        )}
        <DragOverlay dropAnimation={null}>
          {dragPreview?.kind === "asset" ? (
            <AssetDragPreview
              asset={dragPreview.asset}
              active={activeAssetIds.has(dragPreview.asset.ID)}
              count={dragPreview.count}
            />
          ) : dragPreview?.kind === "group" ? (
            <GroupDragPreview group={dragPreview.group} />
          ) : null}
        </DragOverlay>
      </DndContext>
      {contextMenu && (
        <ContextMenu open onOpenChange={(open) => !open && handleContextMenuClose()}>
          <ContextMenuContent
            alignToStylePosition
            style={{
              position: "fixed",
              left: contextMenu.position.x,
              top: contextMenu.position.y,
            }}
            onContextMenu={(event) => event.preventDefault()}
          >
            {contextMenu.kind === "group" && (
              <GroupContextMenuContent
                group={contextMenu.group}
                t={t}
                onAddAsset={onAddAsset}
                onAddGroup={onAddGroup}
                onGroupDetail={onGroupDetail}
                onEditGroup={onEditGroup}
                onMoveGroup={handleMoveGroup}
                onDeleteGroup={handleDeleteGroup}
                onClose={handleContextMenuClose}
              />
            )}
            {contextMenu.kind === "asset" && (
              <AssetContextMenuContent
                asset={contextMenu.asset}
                isConnecting={connectingAssetIds.has(contextMenu.asset.ID)}
                t={t}
                onConnectAsset={onConnectAsset}
                onConnectAssetInNewTab={onConnectAssetInNewTab}
                onOpenFileManager={onOpenFileManager}
                onOpenInfoTab={onOpenInfoTab}
                onEditAsset={onEditAsset}
                onCopyAsset={onCopyAsset}
                onMoveAsset={handleMoveAsset}
                onDeleteAsset={setDeleteAssetConfirm}
                onClose={handleContextMenuClose}
              />
            )}
            {contextMenu.kind === "assets-batch" && (
              <BatchAssetsContextMenuContent
                selectedCount={contextMenu.selectedCount}
                connectableCount={contextMenu.connectableCount}
                t={t}
                onConnectAll={handleRequestBatchConnect}
                onMoveToGroup={handleRequestBatchMove}
                onCopyRefs={handleRequestBatchCopyRefs}
                onDeleteAll={handleRequestBatchDelete}
                onClose={handleContextMenuClose}
              />
            )}
            {contextMenu.kind === "blank" && (
              <BlankContextMenuContent
                t={t}
                onAddGroup={() => onAddGroup(0)}
                onAddAsset={() => onAddAsset(0)}
                onClose={handleContextMenuClose}
              />
            )}
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
            deleteAsset(deleteAssetConfirm.ID).catch((e) => toast.error(errorMessage(e)));
          }
          setDeleteAssetConfirm(null);
        }}
      />
      <ConfirmDialog
        open={!!batchDeleteConfirm}
        onOpenChange={(open) => !open && setBatchDeleteConfirm(null)}
        title={t("asset.deleteSelectedAssetsTitle")}
        description={t("asset.deleteSelectedAssetsDesc", { count: batchDeleteConfirm?.length ?? 0 })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        confirmTestId="confirm-batch-delete-assets"
        onConfirm={() => {
          if (batchDeleteConfirm && batchDeleteConfirm.length > 0) {
            const ids = batchDeleteConfirm.map((a) => a.ID);
            deleteAssets(ids)
              .then(() => notifySuccess(t("asset.deletedAssetsSuccess", { count: ids.length })))
              .catch((e) => toast.error(errorMessage(e)));
          }
          setBatchDeleteConfirm(null);
        }}
      />
      <ConfirmDialog
        open={!!batchConnectConfirm}
        onOpenChange={(open) => !open && setBatchConnectConfirm(null)}
        variant="default"
        title={t("asset.batchConnectTitle")}
        description={t("asset.batchConnectDesc", { count: batchConnectConfirm?.length ?? 0 })}
        cancelText={t("action.cancel")}
        confirmText={t("asset.connectAll")}
        confirmTestId="confirm-batch-connect"
        onConfirm={() => {
          if (batchConnectConfirm) {
            void runBatchConnect(batchConnectConfirm);
          }
          setBatchConnectConfirm(null);
        }}
      />
      <MoveAssetsDialog
        open={!!moveAssetsTarget}
        count={moveAssetsTarget?.length ?? 0}
        onOpenChange={(open) => !open && setMoveAssetsTarget(null)}
        onConfirm={handleConfirmMoveAssets}
      />
    </div>
  );
}

function GroupContextMenuContent({
  group,
  t,
  onAddAsset,
  onAddGroup,
  onGroupDetail,
  onEditGroup,
  onMoveGroup,
  onDeleteGroup,
  onClose,
}: {
  group: group_entity.Group;
  t: (key: string) => string;
  onAddAsset: (groupId: number) => void;
  onAddGroup: (parentId?: number) => void;
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
      <ContextMenuItem
        data-testid="group-context-add-subgroup"
        onClick={() => run(() => onAddGroup(isUngrouped ? 0 : group.ID))}
      >
        <FolderPlus className="h-3.5 w-3.5 mr-1.5" />
        {isUngrouped ? t("asset.addGroup") : t("asset.addSubGroup")}
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

function AssetContextMenuContent({
  asset,
  isConnecting,
  t,
  onConnectAsset,
  onConnectAssetInNewTab,
  onOpenFileManager,
  onOpenInfoTab,
  onEditAsset,
  onCopyAsset,
  onMoveAsset,
  onDeleteAsset,
  onClose,
}: {
  asset: asset_entity.Asset;
  isConnecting: boolean;
  t: (key: string) => string;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onConnectAssetInNewTab?: (asset: asset_entity.Asset) => void;
  onOpenFileManager?: (asset: asset_entity.Asset) => void;
  onOpenInfoTab?: (type: "asset" | "group", id: number, name: string, icon?: string) => void;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onCopyAsset: (asset: asset_entity.Asset) => void;
  onMoveAsset: (id: number, direction: string) => void;
  onDeleteAsset: (asset: asset_entity.Asset) => void;
  onClose: () => void;
}) {
  const run = (action: () => void) => {
    action();
    onClose();
  };

  return (
    <>
      {getAssetType(asset.Type)?.canConnect && (
        <ContextMenuItem onClick={() => run(() => onConnectAsset(asset))} disabled={isConnecting}>
          {isConnecting ? (
            <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
          ) : (
            <TerminalSquare className="h-3.5 w-3.5 mr-1.5" />
          )}
          {t("asset.connect")}
        </ContextMenuItem>
      )}
      {getAssetType(asset.Type)?.canConnectInNewTab && onConnectAssetInNewTab && (
        <ContextMenuItem onClick={() => run(() => onConnectAssetInNewTab(asset))} disabled={isConnecting}>
          <ExternalLink className="h-3.5 w-3.5 mr-1.5" />
          {t("asset.connectInNewTab")}
        </ContextMenuItem>
      )}
      {getAssetType(asset.Type)?.canOpenFileManager && onOpenFileManager && (
        <ContextMenuItem onClick={() => run(() => onOpenFileManager(asset))}>
          <FolderOpen className="h-3.5 w-3.5 mr-1.5" />
          {t("sftp.fileManager")}
        </ContextMenuItem>
      )}
      {onOpenInfoTab && (
        <ContextMenuItem
          data-testid="asset-context-detail"
          onClick={() => run(() => onOpenInfoTab("asset", asset.ID, asset.Name, asset.Icon))}
        >
          <Eye className="h-3.5 w-3.5 mr-1.5" />
          {t("action.editPermission")}
        </ContextMenuItem>
      )}
      <ContextMenuItem data-testid="asset-context-edit" onClick={() => run(() => onEditAsset(asset))}>
        <Pencil className="h-3.5 w-3.5 mr-1.5" />
        {t("action.edit")}
      </ContextMenuItem>
      <ContextMenuItem onClick={() => run(() => onCopyAsset(asset))}>
        <Copy className="h-3.5 w-3.5 mr-1.5" />
        {t("action.copy")}
      </ContextMenuItem>
      <CopyAssetRefMenuItem asset={asset} t={t} onClose={onClose} />
      <ContextMenuSeparator />
      <ContextMenuItem onClick={() => run(() => onMoveAsset(asset.ID, "up"))}>
        <ArrowUp className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveUp")}
      </ContextMenuItem>
      <ContextMenuItem onClick={() => run(() => onMoveAsset(asset.ID, "down"))}>
        <ArrowDown className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveDown")}
      </ContextMenuItem>
      <ContextMenuItem onClick={() => run(() => onMoveAsset(asset.ID, "top"))}>
        <ChevronsUp className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveTop")}
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem
        data-testid="asset-context-delete"
        className="text-destructive"
        onClick={() => run(() => onDeleteAsset(asset))}
      >
        <Trash2 className="h-3.5 w-3.5 mr-1.5" />
        {t("action.delete")}
      </ContextMenuItem>
    </>
  );
}

function BatchAssetsContextMenuContent({
  selectedCount,
  connectableCount,
  t,
  onConnectAll,
  onMoveToGroup,
  onCopyRefs,
  onDeleteAll,
  onClose,
}: {
  selectedCount: number;
  connectableCount: number;
  t: (key: string, opts?: Record<string, unknown>) => string;
  onConnectAll: () => void;
  onMoveToGroup: () => void;
  onCopyRefs: () => void;
  onDeleteAll: () => void;
  onClose: () => void;
}) {
  const binding = useShortcutStore((s) => s.shortcuts["asset.copyRef"]);
  const run = (action: () => void) => {
    action();
    onClose();
  };

  return (
    <>
      <ContextMenuItem
        data-testid="batch-context-connect"
        disabled={connectableCount === 0}
        onClick={() => run(onConnectAll)}
      >
        <TerminalSquare className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.connectAllCount", { count: connectableCount })}
      </ContextMenuItem>
      <ContextMenuItem data-testid="batch-context-move" onClick={() => run(onMoveToGroup)}>
        <FolderInput className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.moveToGroup")}
      </ContextMenuItem>
      <ContextMenuItem data-testid="batch-context-copy-ref" onClick={() => run(onCopyRefs)}>
        <Link2 className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.copyRefs")}
        <ContextMenuShortcut>{formatBinding(binding)}</ContextMenuShortcut>
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem data-testid="batch-context-delete" className="text-destructive" onClick={() => run(onDeleteAll)}>
        <Trash2 className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.deleteSelectedAssets", { count: selectedCount })}
      </ContextMenuItem>
    </>
  );
}

function BlankContextMenuContent({
  t,
  onAddGroup,
  onAddAsset,
  onClose,
}: {
  t: (key: string) => string;
  onAddGroup: () => void;
  onAddAsset: () => void;
  onClose: () => void;
}) {
  const run = (action: () => void) => {
    action();
    onClose();
  };

  return (
    <>
      <ContextMenuItem data-testid="blank-context-add-group" onClick={() => run(onAddGroup)}>
        <FolderPlus className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.addGroup")}
      </ContextMenuItem>
      <ContextMenuItem data-testid="blank-context-add-asset" onClick={() => run(onAddAsset)}>
        <Plus className="h-3.5 w-3.5 mr-1.5" />
        {t("asset.addAsset")}
      </ContextMenuItem>
    </>
  );
}

function AssetDragPreview({
  asset,
  active,
  count = 1,
}: {
  asset: asset_entity.Asset;
  active: boolean;
  count?: number;
}) {
  return (
    <div className="relative">
      {count > 1 && (
        <div className="pointer-events-none absolute inset-0 translate-x-1 translate-y-1 rounded-md border bg-popover/80 shadow-md" />
      )}
      <div className="relative flex min-w-36 max-w-56 items-center gap-1.5 rounded-md border bg-popover px-2 py-1.5 text-sm shadow-lg">
        <EntityIcon
          icon={asset.Icon || undefined}
          fallback={Server}
          className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
        />
        {active && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />}
        <span className="truncate text-popover-foreground">{asset.Name}</span>
        {count > 1 && (
          <span className="ml-auto shrink-0 rounded-full bg-primary px-1.5 py-0.5 text-[11px] font-medium leading-none text-primary-foreground">
            {count}
          </span>
        )}
      </div>
    </div>
  );
}

function GroupDragPreview({ group }: { group: group_entity.Group }) {
  return (
    <div className="flex min-w-36 max-w-56 items-center gap-1.5 rounded-md border bg-popover px-2 py-1.5 text-xs font-medium shadow-lg">
      <EntityIcon
        icon={group.Icon || undefined}
        fallback={Folder}
        className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
      />
      <span className="truncate text-popover-foreground">{group.Name}</span>
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
  onAssetClick: (asset: asset_entity.Asset, event: React.MouseEvent) => void;
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onAddAsset: (groupId: number) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onGroupContextMenu: (group: group_entity.Group, event: React.MouseEvent) => void;
  onAssetContextMenu: (asset: asset_entity.Asset, event: React.MouseEvent) => void;
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
  onAssetClick,
  onSelectAsset,
  onAddAsset,
  onConnectAsset,
  onGroupContextMenu,
  onAssetContextMenu,
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
              onAssetClick={onAssetClick}
              onSelectAsset={onSelectAsset}
              onConnectAsset={onConnectAsset}
              onAssetContextMenu={onAssetContextMenu}
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
      onClick={(e) => {
        e.stopPropagation();
        toggleGroupCollapsed(group.ID);
      }}
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
      onClick={(e) => {
        e.stopPropagation();
        onAddAsset(groupID);
      }}
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
  onAssetClick: (asset: asset_entity.Asset, event: React.MouseEvent) => void;
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onAssetContextMenu: (asset: asset_entity.Asset, event: React.MouseEvent) => void;
};

// Thin wrapper: only this component subscribes to DndContext. It re-renders on every
// drag move, but its body is just a div + children passthrough, and `children` is the
// same React element reference as long as AssetRowContent's props don't change, so
// React's reconciler skips the heavy subtree.
function AssetRowDraggable({ id, assetID, children }: { id: string; assetID: number; children: React.ReactNode }) {
  const { setNodeRef, attributes, listeners, isDragging } = useDraggable({ id });
  const isPartOfDrag = useAssetTreeDndStore((s) => s.draggingAssetIDSet.has(assetID));
  const dimmed = isDragging || isPartOfDrag;
  return (
    <div
      ref={setNodeRef}
      data-asset-tree-row={id}
      className="relative"
      style={{
        zIndex: isDragging ? 20 : undefined,
        opacity: dimmed ? 0.5 : undefined,
      }}
      {...attributes}
      {...listeners}
    >
      {children}
    </div>
  );
}

function CopyAssetRefMenuItem({
  asset,
  t,
  onClose,
}: {
  asset: asset_entity.Asset;
  t: (key: string) => string;
  onClose: () => void;
}) {
  const binding = useShortcutStore((s) => s.shortcuts["asset.copyRef"]);
  return (
    <ContextMenuItem
      data-testid="asset-context-copy-ref"
      onClick={() => {
        onClose();
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
    <AssetRowDraggable id={`asset-${props.asset.ID}`} assetID={props.asset.ID}>
      <AssetRowContent {...props} />
    </AssetRowDraggable>
  );
});

const AssetRowContent = React.memo(function AssetRowContent({
  asset,
  depth,
  active,
  connecting: isConnecting,
  onAssetClick,
  onSelectAsset,
  onConnectAsset,
  onAssetContextMenu,
}: AssetRowProps) {
  // Per-row boolean selector: a selection change re-renders only the rows whose state flips.
  const selected = useAssetStore((s) => s.selectedAssetId === asset.ID || s.selectedAssetIdSet.has(asset.ID));
  const style: React.CSSProperties = {
    paddingLeft: `${20 + depth * 12}px`,
  };

  return (
    <div
      data-selected={selected ? "true" : undefined}
      aria-selected={selected}
      className={`flex items-center gap-1.5 rounded-md pr-2 py-1.5 text-sm outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring/60 cursor-pointer select-none transition-colors duration-150 ${
        selected
          ? "bg-primary/15 text-sidebar-accent-foreground ring-1 ring-inset ring-primary/30"
          : "hover:bg-sidebar-accent"
      }`}
      style={style}
      onClick={(e) => onAssetClick(asset, e)}
      onContextMenu={(e) => onAssetContextMenu(asset, e)}
      onDoubleClick={(e) => {
        // 修饰键多选点击时抑制双击触发连接，避免连续 Ctrl/Shift+Click 误连。
        if (e.ctrlKey || e.metaKey || e.shiftKey) return;
        onSelectAsset(asset);
        // 连接中也照常发起：已有标签（含连接中）由打开逻辑切过去，不会重复连接（#380）
        if (getAssetType(asset.Type)?.canConnect) onConnectAsset(asset);
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
  );
});
