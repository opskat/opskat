import { useEffect, useState } from "react";
import { useFullscreen } from "@/hooks/useFullscreen";
import {
  ChevronRight,
  ChevronDown,
  Folder,
  Server,
  Plus,
  FolderPlus,
  Search,
  Loader2,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogCancel,
  AlertDialogAction,
} from "@/components/ui/alert-dialog";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { getIconComponent } from "@/components/asset/IconPicker";
import { useAssetStore } from "@/stores/assetStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { asset_entity, group_entity } from "../../../wailsjs/go/models";

interface AssetTreeProps {
  collapsed: boolean;
  onAddAsset: (groupId?: number) => void;
  onAddGroup: () => void;
  onEditGroup: (group: group_entity.Group) => void;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onCopyAsset: (asset: asset_entity.Asset) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onSelectAsset: (asset: asset_entity.Asset) => void;
}

export function AssetTree({
  collapsed,
  onAddAsset,
  onAddGroup,
  onEditGroup,
  onEditAsset,
  onCopyAsset,
  onConnectAsset,
  onSelectAsset,
}: AssetTreeProps) {
  const { t } = useTranslation();
  const isFullscreen = useFullscreen();
  const { assets, groups, selectedAssetId, fetchAssets, fetchGroups, deleteAsset, deleteGroup } =
    useAssetStore();
  const { tabs, connectingAssetIds } = useTerminalStore();
  const [filter, setFilter] = useState("");
  const [deleteConfirm, setDeleteConfirm] = useState<{
    id: number;
    assetCount: number;
  } | null>(null);

  useEffect(() => {
    fetchAssets();
    fetchGroups();
  }, [fetchAssets, fetchGroups]);

  if (collapsed) return null;

  const connectedAssetIds = new Set(
    tabs.filter((t) => Object.values(t.panes).some((p) => p.connected)).map((t) => t.assetId)
  );

  const filteredAssets = filter
    ? assets.filter((a) =>
        a.Name.toLowerCase().includes(filter.toLowerCase())
      )
    : assets;

  // Group assets by GroupID
  const groupedAssets = new Map<number, asset_entity.Asset[]>();
  for (const asset of filteredAssets) {
    const gid = asset.GroupID || 0;
    if (!groupedAssets.has(gid)) groupedAssets.set(gid, []);
    groupedAssets.get(gid)!.push(asset);
  }

  const childGroups = (parentId: number) =>
    groups.filter((g) => (g.ParentID || 0) === parentId);

  const countAssetsInGroup = (groupId: number): number => {
    let count = (groupedAssets.get(groupId) || []).length;
    for (const child of childGroups(groupId)) {
      count += countAssetsInGroup(child.ID);
    }
    return count;
  };

  const handleDeleteGroup = (id: number) => {
    const directAssetCount = (groupedAssets.get(id) || []).length;
    if (directAssetCount > 0) {
      setDeleteConfirm({ id, assetCount: directAssetCount });
    } else {
      deleteGroup(id, false).catch((e) => toast.error(String(e)));
    }
  };

  const handleConfirmDelete = async (deleteAssets: boolean) => {
    if (!deleteConfirm) return;
    try {
      await deleteGroup(deleteConfirm.id, deleteAssets);
    } catch (e) {
      toast.error(String(e));
    }
    setDeleteConfirm(null);
  };

  return (
    <div className="flex h-full w-56 flex-col border-r border-panel-divider bg-sidebar">
      {/* Drag region for frameless window */}
      <div
        className={`${isFullscreen ? "h-2" : "h-10"} w-full shrink-0`}
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
      />
      <div className="flex flex-col gap-1.5 px-3 pb-2 border-b border-panel-divider">
        <div className="flex items-center justify-between">
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {t("asset.title")}
          </span>
          <div className="flex gap-0.5">
            <Button
              variant="ghost"
              size="icon"
              className="h-6 w-6"
              onClick={() => onAddGroup()}
              title={t("asset.addGroup")}
            >
              <FolderPlus className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="h-6 w-6"
              onClick={() => onAddAsset()}
              title={t("asset.addAsset")}
            >
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground" />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t("asset.search") || "Search..."}
            className="h-7 w-full rounded-md border border-sidebar-border bg-sidebar pl-7 pr-2 text-xs outline-none focus:border-ring focus:ring-1 focus:ring-ring/50 placeholder:text-muted-foreground/60 transition-colors duration-150"
          />
        </div>
      </div>
      <ScrollArea className="flex-1 min-h-0">
        <div className="p-2 space-y-0.5">
          {childGroups(0).map((group) => (
            <GroupItem
              key={group.ID}
              group={group}
              assets={groupedAssets.get(group.ID) || []}
              allGroupedAssets={groupedAssets}
              childGroups={childGroups}
              countAssetsInGroup={countAssetsInGroup}
              selectedAssetId={selectedAssetId}
              connectedAssetIds={connectedAssetIds}
              connectingAssetIds={connectingAssetIds}
              onSelectAsset={onSelectAsset}
              onAddAsset={() => onAddAsset(group.ID)}
              onEditAsset={onEditAsset}
              onCopyAsset={onCopyAsset}
              onConnectAsset={onConnectAsset}
              onEditGroup={onEditGroup}
              onDeleteGroup={handleDeleteGroup}
              onDeleteAsset={deleteAsset}
              depth={0}
              t={t}
            />
          ))}
          {(groupedAssets.get(0) || []).length > 0 && (
            <GroupItem
              group={
                new group_entity.Group({
                  ID: 0,
                  Name: t("asset.ungrouped"),
                })
              }
              assets={groupedAssets.get(0) || []}
              allGroupedAssets={groupedAssets}
              childGroups={() => []}
              countAssetsInGroup={() => (groupedAssets.get(0) || []).length}
              selectedAssetId={selectedAssetId}
              connectedAssetIds={connectedAssetIds}
              connectingAssetIds={connectingAssetIds}
              onSelectAsset={onSelectAsset}
              onAddAsset={() => onAddAsset(0)}
              onEditAsset={onEditAsset}
              onCopyAsset={onCopyAsset}
              onConnectAsset={onConnectAsset}
              onEditGroup={onEditGroup}
              onDeleteGroup={handleDeleteGroup}
              onDeleteAsset={deleteAsset}
              depth={0}
              t={t}
            />
          )}
          {filteredAssets.length === 0 && groups.length === 0 && (
            <p className="text-xs text-muted-foreground text-center py-4">
              {t("asset.addAsset")}
            </p>
          )}
        </div>
      </ScrollArea>
      <AlertDialog
        open={!!deleteConfirm}
        onOpenChange={(open) => !open && setDeleteConfirm(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("asset.deleteGroupTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("asset.deleteGroupDesc", { count: deleteConfirm?.assetCount })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("action.cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={() => handleConfirmDelete(false)}>
              {t("asset.moveToUngrouped")}
            </AlertDialogAction>
            <AlertDialogAction
              variant="destructive"
              onClick={() => handleConfirmDelete(true)}
            >
              {t("asset.deleteAssets")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function GroupItem({
  group,
  assets,
  allGroupedAssets,
  childGroups,
  countAssetsInGroup,
  selectedAssetId,
  connectedAssetIds,
  connectingAssetIds,
  onSelectAsset,
  onAddAsset,
  onEditAsset,
  onCopyAsset,
  onConnectAsset,
  onEditGroup,
  onDeleteGroup,
  onDeleteAsset,
  depth,
  t,
}: {
  group: group_entity.Group;
  assets: asset_entity.Asset[];
  allGroupedAssets: Map<number, asset_entity.Asset[]>;
  childGroups: (parentId: number) => group_entity.Group[];
  countAssetsInGroup: (groupId: number) => number;
  selectedAssetId: number | null;
  connectedAssetIds: Set<number>;
  connectingAssetIds: Set<number>;
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onAddAsset: () => void;
  onEditAsset: (asset: asset_entity.Asset) => void;
  onCopyAsset: (asset: asset_entity.Asset) => void;
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onEditGroup: (group: group_entity.Group) => void;
  onDeleteGroup: (id: number) => void;
  onDeleteAsset: (id: number) => void;
  depth: number;
  t: (key: string) => string;
}) {
  const [expanded, setExpanded] = useState(true);
  const children = group.ID > 0 ? childGroups(group.ID) : [];
  const totalCount = countAssetsInGroup(group.ID);
  const GroupIcon = group.Icon ? getIconComponent(group.Icon) : Folder;

  const groupRow = (
    <div
      className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium hover:bg-sidebar-accent cursor-pointer transition-colors duration-150"
      style={{ paddingLeft: `${8 + depth * 12}px` }}
      onClick={() => setExpanded(!expanded)}
    >
      {expanded ? (
        <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      ) : (
        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      )}
      <GroupIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="truncate text-sidebar-foreground">{group.Name}</span>
      <span className="ml-auto text-xs text-muted-foreground">
        {totalCount}
      </span>
    </div>
  );

  return (
    <div>
      {group.ID > 0 ? (
        <ContextMenu>
          <ContextMenuTrigger>{groupRow}</ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuItem onClick={() => onEditGroup(group)}>
              {t("action.edit")}
            </ContextMenuItem>
            <ContextMenuItem
              className="text-destructive"
              onClick={() => onDeleteGroup(group.ID)}
            >
              {t("action.delete")}
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
      ) : (
        groupRow
      )}
      <div
        className="tree-group-content"
        data-collapsed={!expanded ? "true" : undefined}
      >
        <div>
          {children.map((child) => (
            <GroupItem
              key={child.ID}
              group={child}
              assets={allGroupedAssets.get(child.ID) || []}
              allGroupedAssets={allGroupedAssets}
              childGroups={childGroups}
              countAssetsInGroup={countAssetsInGroup}
              selectedAssetId={selectedAssetId}
              connectedAssetIds={connectedAssetIds}
              connectingAssetIds={connectingAssetIds}
              onSelectAsset={onSelectAsset}
              onAddAsset={onAddAsset}
              onEditAsset={onEditAsset}
              onCopyAsset={onCopyAsset}
              onConnectAsset={onConnectAsset}
              onEditGroup={onEditGroup}
              onDeleteGroup={onDeleteGroup}
              onDeleteAsset={onDeleteAsset}
              depth={depth + 1}
              t={t}
            />
          ))}
          {assets.map((asset) => {
            const AssetIcon = asset.Icon ? getIconComponent(asset.Icon) : Server;
            const isConnecting = connectingAssetIds.has(asset.ID);
            return (
              <ContextMenu key={asset.ID}>
                <ContextMenuTrigger>
                  <div
                    className={`flex items-center gap-1.5 rounded-md pr-2 py-1.5 text-sm cursor-pointer select-none transition-colors duration-150 ${
                      selectedAssetId === asset.ID
                        ? "bg-sidebar-accent text-sidebar-accent-foreground"
                        : "hover:bg-sidebar-accent"
                    }`}
                    style={{ paddingLeft: `${20 + (depth + 1) * 12}px` }}
                    onClick={() => onSelectAsset(asset)}
                    onDoubleClick={() => !isConnecting && onConnectAsset(asset)}
                  >
                    {isConnecting ? (
                      <Loader2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground animate-spin" />
                    ) : (
                      <AssetIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    )}
                    {connectedAssetIds.has(asset.ID) && (
                      <span className="h-1.5 w-1.5 rounded-full bg-success shrink-0" />
                    )}
                    <span className="truncate text-sidebar-foreground">
                      {asset.Name}
                    </span>
                  </div>
                </ContextMenuTrigger>
                <ContextMenuContent>
                  <ContextMenuItem onClick={() => onConnectAsset(asset)} disabled={isConnecting}>
                    {isConnecting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                    {t("asset.connect")}
                  </ContextMenuItem>
                  <ContextMenuItem onClick={() => onEditAsset(asset)}>
                    {t("action.edit")}
                  </ContextMenuItem>
                  <ContextMenuItem onClick={() => onCopyAsset(asset)}>
                    {t("action.copy")}
                  </ContextMenuItem>
                  <ContextMenuItem
                    className="text-destructive"
                    onClick={() => onDeleteAsset(asset.ID)}
                  >
                    {t("action.delete")}
                  </ContextMenuItem>
                </ContextMenuContent>
              </ContextMenu>
            );
          })}
          {assets.length === 0 && children.length === 0 && (
            <div
              className="pr-2 py-1 text-xs text-muted-foreground cursor-pointer hover:underline"
              style={{ paddingLeft: `${20 + (depth + 1) * 12}px` }}
              onClick={onAddAsset}
            >
              + {t("asset.addAsset")}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
