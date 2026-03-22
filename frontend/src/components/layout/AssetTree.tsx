import { useEffect, useState } from "react";
import {
  ChevronRight,
  ChevronDown,
  Folder,
  Server,
  Plus,
  FolderPlus,
  Search,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { useAssetStore } from "@/stores/assetStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { asset_entity, group_entity } from "../../../wailsjs/go/models";

interface AssetTreeProps {
  collapsed: boolean;
  onAddAsset: (groupId?: number) => void;
  onAddGroup: () => void;
  onSelectAsset: (asset: asset_entity.Asset) => void;
}

export function AssetTree({
  collapsed,
  onAddAsset,
  onAddGroup,
  onSelectAsset,
}: AssetTreeProps) {
  const { t } = useTranslation();
  const { assets, groups, selectedAssetId, fetchAssets, fetchGroups, deleteAsset } =
    useAssetStore();
  const { tabs } = useTerminalStore();
  const [filter, setFilter] = useState("");

  useEffect(() => {
    fetchAssets();
    fetchGroups();
  }, [fetchAssets, fetchGroups]);

  if (collapsed) return null;

  // Connected asset IDs
  const connectedAssetIds = new Set(
    tabs.filter((t) => t.connected).map((t) => t.assetId)
  );

  // Filter assets by name
  const filteredAssets = filter
    ? assets.filter((a) =>
        a.Name.toLowerCase().includes(filter.toLowerCase())
      )
    : assets;

  // Group assets
  const groupedAssets = new Map<number, asset_entity.Asset[]>();
  for (const asset of filteredAssets) {
    const gid = asset.GroupID || 0;
    if (!groupedAssets.has(gid)) groupedAssets.set(gid, []);
    groupedAssets.get(gid)!.push(asset);
  }

  return (
    <div className="flex h-full w-56 flex-col border-r border-sidebar-border bg-sidebar">
      <div className="flex flex-col gap-1.5 px-3 py-2 border-b border-sidebar-border">
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
      <ScrollArea className="flex-1">
        <div className="p-2 space-y-0.5">
          {groups.map((group) => (
            <GroupItem
              key={group.ID}
              group={group}
              assets={groupedAssets.get(group.ID) || []}
              selectedAssetId={selectedAssetId}
              connectedAssetIds={connectedAssetIds}
              onSelectAsset={onSelectAsset}
              onAddAsset={() => onAddAsset(group.ID)}
              onDeleteAsset={deleteAsset}
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
              selectedAssetId={selectedAssetId}
              connectedAssetIds={connectedAssetIds}
              onSelectAsset={onSelectAsset}
              onAddAsset={() => onAddAsset(0)}
              onDeleteAsset={deleteAsset}
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
    </div>
  );
}

function GroupItem({
  group,
  assets,
  selectedAssetId,
  connectedAssetIds,
  onSelectAsset,
  onAddAsset,
  onDeleteAsset,
  t,
}: {
  group: group_entity.Group;
  assets: asset_entity.Asset[];
  selectedAssetId: number | null;
  connectedAssetIds: Set<number>;
  onSelectAsset: (asset: asset_entity.Asset) => void;
  onAddAsset: () => void;
  onDeleteAsset: (id: number) => void;
  t: (key: string) => string;
}) {
  const [expanded, setExpanded] = useState(true);

  return (
    <div>
      <div
        className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium hover:bg-sidebar-accent cursor-pointer transition-colors duration-150"
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? (
          <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        )}
        <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate text-sidebar-foreground">{group.Name}</span>
        <span className="ml-auto text-xs text-muted-foreground">
          {assets.length}
        </span>
      </div>
      <div
        className="tree-group-content"
        data-collapsed={!expanded ? "true" : undefined}
      >
        <div>
          {assets.map((asset) => (
            <ContextMenu key={asset.ID}>
              <ContextMenuTrigger>
                <div
                  className={`flex items-center gap-1.5 rounded-md pl-7 pr-2 py-1.5 text-sm cursor-pointer transition-colors duration-150 ${
                    selectedAssetId === asset.ID
                      ? "bg-sidebar-accent text-sidebar-accent-foreground"
                      : "hover:bg-sidebar-accent"
                  }`}
                  onClick={() => onSelectAsset(asset)}
                >
                  <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  {connectedAssetIds.has(asset.ID) && (
                    <span className="h-1.5 w-1.5 rounded-full bg-success shrink-0" />
                  )}
                  <span className="truncate text-sidebar-foreground">
                    {asset.Name}
                  </span>
                </div>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem onClick={() => onSelectAsset(asset)}>
                  {t("action.edit")}
                </ContextMenuItem>
                <ContextMenuItem
                  className="text-destructive"
                  onClick={() => onDeleteAsset(asset.ID)}
                >
                  {t("action.delete")}
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          ))}
          {assets.length === 0 && (
            <div
              className="pl-7 pr-2 py-1 text-xs text-muted-foreground cursor-pointer hover:underline"
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
