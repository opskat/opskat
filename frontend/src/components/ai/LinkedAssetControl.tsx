import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { X } from "lucide-react";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const setAsset = useAIStore((s) => s.setSidebarTabAsset);
  const clearAsset = useAIStore((s) => s.clearSidebarTabAsset);
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
    return (
      <div className="flex items-center gap-2" data-testid="linked-asset-chip">
        <span className="inline-flex items-center gap-1.5 rounded-md border border-border bg-secondary px-2 py-0.5 text-xs">
          <span className="h-1.5 w-1.5 rounded-full bg-success" />
          {tab.linkedAssetName}
        </span>
        <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => setPicking(true)}>
          {t("ai.sidebar.linkedAsset.change")}
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="h-6 w-6"
          data-testid="linked-asset-clear"
          onClick={() => clearAsset(sidebarTabId)}
          title={t("ai.sidebar.linkedAsset.clear")}
          aria-label={t("ai.sidebar.linkedAsset.clear")}
        >
          <X className="h-3.5 w-3.5" />
        </Button>
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
