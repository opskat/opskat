import { useMemo } from "react";
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
import { Check, ChevronDown, CircleDashed, Link2, X } from "lucide-react";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";
import { tabToAssetRef } from "@/lib/tabAsset";
import { AssetIcon } from "@/components/asset/AssetIcon";

type OpenTab = { tabId: string; label: string; assetId: number; assetName: string; assetType: string };

/** 绑定控件:chip 触发的下拉(已打开的标签页列表 + 清除 + 联动开关)。绑定到 tab 实例,联动开关闸控双向导航同步。 */
export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const bindTab = useAIStore((s) => s.bindSidebarTab);
  const unbindTab = useAIStore((s) => s.unbindSidebarTab);
  const setSync = useAIStore((s) => s.setSidebarTabSync);
  const assets = useAssetStore((s) => s.assets);
  const tabs = useTabStore((s) => s.tabs);

  // 已打开的工作区 tab → 资产引用,按 tab 逐个列出(同一资产多开各列一项,靠 tab 标题区分)。
  const openTabs = useMemo(() => {
    const out: OpenTab[] = [];
    for (const wt of tabs) {
      const ref = tabToAssetRef(wt);
      if (!ref) continue;
      out.push({ tabId: wt.id, label: wt.label, ...ref });
    }
    return out;
  }, [tabs]);

  if (!sidebarTabId) return null;

  const bound = tab?.linkedAssetId != null;
  const tabLive = tab?.linkedTabId != null && tabs.some((wt) => wt.id === tab.linkedTabId);
  const syncing = !!tab?.syncTab;
  const syncTitle = syncing ? t("ai.sidebar.syncing") : undefined;

  const bindToTab = (item: OpenTab) =>
    bindTab(sidebarTabId, {
      workspaceTabId: item.tabId,
      assetId: item.assetId,
      assetName: item.assetName,
      assetType: item.assetType,
    });

  const triggerLabel = bound
    ? syncTitle
      ? `${tab?.linkedAssetName} · ${syncTitle}`
      : tab?.linkedAssetName || undefined
    : t("ai.sidebar.linkedAsset.pickPlaceholder");

  return (
    <div className="flex items-center gap-2" data-testid="linked-asset-chip">
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="linked-asset-menu-trigger"
            title={syncTitle}
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
                {syncing && <Link2 className="h-3 w-3 text-primary" />}
                <span className={cn("h-1.5 w-1.5 rounded-full", tabLive ? "bg-success" : "bg-muted-foreground/50")} />
                <AssetIcon
                  assets={assets}
                  assetId={tab?.linkedAssetId}
                  fallbackType={tab?.linkedAssetType}
                  className="h-3.5 w-3.5"
                />
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
          <DropdownMenuLabel className="px-2 py-1 text-[11px] font-semibold text-muted-foreground/70">
            {t("ai.sidebar.linkedAsset.openTabs")}
          </DropdownMenuLabel>
          {openTabs.length === 0 ? (
            <div data-testid="menu-no-open-tabs" className="px-2 py-1.5 text-xs text-muted-foreground/60">
              {t("ai.sidebar.linkedAsset.noOpenTabs")}
            </div>
          ) : (
            openTabs.map((item) => {
              const isBound = item.tabId === tab?.linkedTabId;
              return (
                <DropdownMenuItem
                  key={item.tabId}
                  data-testid={`menu-tab-${item.tabId}`}
                  onSelect={() => bindToTab(item)}
                  className={cn("gap-2", isBound && "bg-primary/10")}
                >
                  <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                  <AssetIcon
                    assets={assets}
                    assetId={item.assetId}
                    fallbackType={item.assetType}
                    className="h-3.5 w-3.5"
                  />
                  <span className="flex-1 truncate">{item.label}</span>
                  {isBound && <Check className="h-3.5 w-3.5 text-primary" />}
                </DropdownMenuItem>
              );
            })
          )}
          {bound && (
            <DropdownMenuItem data-testid="menu-clear" onSelect={() => unbindTab(sidebarTabId)}>
              <X className="h-3.5 w-3.5" />
              {t("ai.sidebar.linkedAsset.clearBinding")}
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            data-testid="menu-sync"
            disabled={!tabLive}
            onSelect={(e) => {
              e.preventDefault();
              if (tabLive) setSync(sidebarTabId, !syncing);
            }}
            className="gap-2"
          >
            <Link2 className="h-3.5 w-3.5" />
            <span className="flex-1">{t("ai.sidebar.sync")}</span>
            <Switch checked={syncing} aria-hidden tabIndex={-1} className="pointer-events-none" />
          </DropdownMenuItem>
          <div className="px-2 py-1 text-[11px] leading-4 text-muted-foreground/60">
            {bound && !tabLive ? t("ai.sidebar.linkedAsset.tabClosed") : t("ai.sidebar.linkedAsset.syncHint")}
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
