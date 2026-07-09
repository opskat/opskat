import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  cn,
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  Switch,
} from "@opskat/ui";
import { Check, ChevronDown, CircleDashed, Link2, Plus, X } from "lucide-react";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";
import { getAssetType } from "@/lib/assetTypes";
import { tabToAssetRef } from "@/lib/tabAsset";

type AssetRef = { assetId: number; assetName: string; assetType: string };

/** 绑定主资产控件：chip 触发的下拉菜单（已打开终端列表 + 资产库 + 清除 + 跟随开关）。 */
export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const setAsset = useAIStore((s) => s.setSidebarTabAsset);
  const clearAsset = useAIStore((s) => s.clearSidebarTabAsset);
  const setFollow = useAIStore((s) => s.setSidebarTabFollow);
  const assets = useAssetStore((s) => s.assets);
  const tabs = useTabStore((s) => s.tabs);
  const [picking, setPicking] = useState(false);

  // 已打开的工作区 tab → 资产引用，按资产去重（同一资产多开只列一次）。
  const openTerminals = useMemo(() => {
    const seen = new Set<number>();
    const out: AssetRef[] = [];
    for (const wt of tabs) {
      const ref = tabToAssetRef(wt);
      if (!ref || seen.has(ref.assetId)) continue;
      seen.add(ref.assetId);
      out.push(ref);
    }
    return out;
  }, [tabs]);

  if (!sidebarTabId) return null;

  const bound = tab?.linkedAssetId != null;
  const following = !!tab?.followActiveTerminal;
  const followTitle = following ? t("ai.sidebar.following") : undefined;
  const BoundIcon = bound && tab?.linkedAssetType ? getAssetType(tab.linkedAssetType)?.icon : undefined;

  const bind = (ref: AssetRef) => setAsset(sidebarTabId, ref);

  // 资产库选择器只回传 id；名称/类型从 assetStore 补全。
  const handleLibraryPick = (assetId: number) => {
    const asset = assets.find((a) => a.ID === assetId);
    if (asset) bind({ assetId, assetName: asset.Name, assetType: asset.Type });
    setPicking(false);
  };

  const triggerLabel = bound
    ? followTitle
      ? `${tab?.linkedAssetName} · ${followTitle}`
      : tab?.linkedAssetName || undefined
    : t("ai.sidebar.linkedAsset.pickPlaceholder");

  return (
    <div className="flex items-center gap-2" data-testid="linked-asset-chip">
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="linked-asset-menu-trigger"
            title={followTitle}
            aria-label={triggerLabel}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs",
              bound
                ? "border-primary/40 bg-primary/10 text-foreground"
                : "border-border bg-muted/30 text-muted-foreground"
            )}
          >
            {bound ? (
              <>
                {following && <Link2 className="h-3 w-3 text-primary" />}
                <span className="h-1.5 w-1.5 rounded-full bg-success" />
                {BoundIcon && <BoundIcon className="h-3.5 w-3.5 text-primary/80" />}
                <span className="max-w-[140px] truncate">{tab?.linkedAssetName}</span>
              </>
            ) : (
              <>
                <CircleDashed className="h-3.5 w-3.5" />
                <span className="max-w-[160px] truncate">{t("ai.sidebar.linkedAsset.pickPlaceholder")}</span>
              </>
            )}
            <ChevronDown className="h-3 w-3 opacity-60" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-[220px]" onCloseAutoFocus={(e) => e.preventDefault()}>
          {openTerminals.length > 0 && (
            <>
              <DropdownMenuLabel className="px-2 py-1 text-[11px] font-semibold text-muted-foreground/70">
                {t("ai.sidebar.linkedAsset.openTerminals")}
              </DropdownMenuLabel>
              {openTerminals.map((ref) => {
                const Icon = getAssetType(ref.assetType)?.icon;
                const isBound = ref.assetId === tab?.linkedAssetId;
                return (
                  <DropdownMenuItem
                    key={ref.assetId}
                    data-testid={`menu-terminal-${ref.assetId}`}
                    onSelect={() => bind(ref)}
                    className={cn("gap-2", isBound && "bg-primary/10")}
                  >
                    <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                    {Icon && <Icon className="h-3.5 w-3.5" />}
                    <span className="flex-1 truncate">{ref.assetName}</span>
                    {isBound && <Check className="h-3.5 w-3.5 text-primary" />}
                  </DropdownMenuItem>
                );
              })}
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuItem data-testid="menu-pick-library" onSelect={() => setPicking(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t("ai.sidebar.linkedAsset.pickFromLibrary")}
          </DropdownMenuItem>
          {bound && (
            <DropdownMenuItem data-testid="menu-clear" onSelect={() => clearAsset(sidebarTabId)}>
              <X className="h-3.5 w-3.5" />
              {t("ai.sidebar.linkedAsset.clearBinding")}
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            data-testid="menu-follow"
            disabled={!bound}
            onSelect={(e) => {
              e.preventDefault();
              if (bound) setFollow(sidebarTabId, !following);
            }}
            className="gap-2"
          >
            <Link2 className="h-3.5 w-3.5" />
            <span className="flex-1">{t("ai.sidebar.follow")}</span>
            <Switch checked={following} aria-hidden tabIndex={-1} className="pointer-events-none" />
          </DropdownMenuItem>
          <div className="px-2 py-1 text-[11px] leading-4 text-muted-foreground/60">
            {t("ai.sidebar.linkedAsset.followHint")}
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
      {picking && (
        <AssetSelect
          value={tab?.linkedAssetId ?? 0}
          onValueChange={handleLibraryPick}
          placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")}
          testId="linked-asset-picker"
        />
      )}
    </div>
  );
}
