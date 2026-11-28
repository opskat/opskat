import React from "react";
import { FolderInput, Link2, TerminalSquare, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button, Tooltip, TooltipContent, TooltipTrigger } from "@opskat/ui";
import { useAssetStore } from "@/stores/assetStore";
import { getAssetType } from "@/lib/assetTypes";

interface AssetSelectionBarProps {
  onConnectSelected: () => void;
  onMoveSelected: () => void;
  onCopyRefsSelected: () => void;
  onDeleteSelected: () => void;
  onClearSelection: () => void;
}

/**
 * 隔离的多选操作条：外层仅订阅选中数量，在未多选（0 或 1 项）时不挂载内层，
 * 确保单选切换不引起 AssetTree 根节点或无关行的重绘。
 */
export const AssetSelectionBar = React.memo(function AssetSelectionBar(props: AssetSelectionBarProps) {
  const selectedCount = useAssetStore((s) => s.selectedAssetIds.length);
  if (selectedCount < 2) return null;
  return <AssetSelectionBarInner {...props} selectedCount={selectedCount} />;
});

function AssetSelectionBarInner({
  selectedCount,
  onConnectSelected,
  onMoveSelected,
  onCopyRefsSelected,
  onDeleteSelected,
  onClearSelection,
}: AssetSelectionBarProps & { selectedCount: number }) {
  const { t } = useTranslation();
  const connectableCount = useAssetStore((s) => {
    let count = 0;
    for (const asset of s.assets) {
      if (s.selectedAssetIdSet.has(asset.ID) && getAssetType(asset.Type)?.canConnect) {
        count++;
      }
    }
    return count;
  });

  return (
    <div
      data-testid="asset-selection-bar"
      className="flex items-center justify-between gap-1 border-b border-panel-divider bg-primary/10 px-2.5 py-1 text-xs"
    >
      <span className="truncate font-medium text-foreground">{t("asset.selectedCount", { count: selectedCount })}</span>
      <div className="flex items-center gap-0.5 shrink-0">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              data-testid="selection-bar-connect"
              disabled={connectableCount === 0}
              onClick={onConnectSelected}
              aria-label={t("asset.connectAll")}
            >
              <TerminalSquare className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{t("asset.connectAllCount", { count: connectableCount })}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              data-testid="selection-bar-move"
              onClick={onMoveSelected}
              aria-label={t("asset.moveToGroup")}
            >
              <FolderInput className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{t("asset.moveToGroup")}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              data-testid="selection-bar-copy-ref"
              onClick={onCopyRefsSelected}
              aria-label={t("asset.copyRefs")}
            >
              <Link2 className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{t("asset.copyRefs")}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              data-testid="selection-bar-delete"
              className="text-destructive hover:text-destructive"
              onClick={onDeleteSelected}
              aria-label={t("action.delete")}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{t("asset.deleteSelectedAssets", { count: selectedCount })}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              data-testid="selection-bar-clear"
              onClick={onClearSelection}
              aria-label={t("asset.clearSelection")}
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{t("asset.clearSelection")}</TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}
