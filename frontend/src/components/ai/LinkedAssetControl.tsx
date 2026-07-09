import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuCheckboxItem,
} from "@opskat/ui";
import { ChevronDown, Link2 } from "lucide-react";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const setAsset = useAIStore((s) => s.setSidebarTabAsset);
  const clearAsset = useAIStore((s) => s.clearSidebarTabAsset);
  const setFollow = useAIStore((s) => s.setSidebarTabFollow);
  const assets = useAssetStore((s) => s.assets);
  const [picking, setPicking] = useState(false);

  if (!sidebarTabId) return null;

  const handlePick = (assetId: number) => {
    const asset = assets.find((a) => a.ID === assetId);
    if (!asset) return;
    setAsset(sidebarTabId, { assetId, assetName: asset.Name, assetType: asset.Type });
    setPicking(false);
  };

  if (tab?.linkedAssetId != null) {
    const followTitle = tab.followActiveTerminal ? t("ai.sidebar.following") : undefined;
    const triggerLabel = followTitle ? `${tab.linkedAssetName} · ${followTitle}` : tab.linkedAssetName || undefined;
    return (
      <div className="flex items-center gap-2" data-testid="linked-asset-chip">
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              data-testid="linked-asset-menu-trigger"
              title={followTitle}
              aria-label={triggerLabel}
              className="inline-flex items-center gap-1.5 rounded-md border border-border bg-secondary px-2 py-0.5 text-xs"
            >
              {tab.followActiveTerminal ? (
                <Link2 className="h-3 w-3 text-primary" />
              ) : (
                <span className="h-1.5 w-1.5 rounded-full bg-success" />
              )}
              <span className="max-w-[140px] truncate">{tab.linkedAssetName}</span>
              <ChevronDown className="h-3 w-3 opacity-60" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-[180px]" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem data-testid="menu-change" onSelect={() => setPicking(true)}>
              {t("ai.sidebar.linkedAsset.change")}
            </DropdownMenuItem>
            <DropdownMenuItem data-testid="menu-clear" onSelect={() => clearAsset(sidebarTabId)}>
              {t("ai.sidebar.linkedAsset.clear")}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuCheckboxItem
              data-testid="menu-follow"
              checked={!!tab.followActiveTerminal}
              onCheckedChange={(v) => setFollow(sidebarTabId, !!v)}
            >
              {t("ai.sidebar.follow")}
            </DropdownMenuCheckboxItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {picking && (
          <AssetSelect
            value={tab.linkedAssetId}
            onValueChange={handlePick}
            placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")}
            testId="linked-asset-picker"
          />
        )}
      </div>
    );
  }

  return (
    <div data-testid="linked-asset-bind-row" className="flex items-center gap-2">
      <span className="text-xs text-muted-foreground">{t("ai.sidebar.linkedAsset.unbound")}</span>
      <AssetSelect
        value={0}
        onValueChange={handlePick}
        placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")}
        testId="linked-asset-picker"
      />
    </div>
  );
}
