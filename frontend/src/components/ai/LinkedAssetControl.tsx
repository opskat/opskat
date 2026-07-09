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
import { tabToAssetRef } from "@/lib/tabAsset";
import { resolveAssetIcon } from "@/lib/aiAssetIcon";

type OpenTerminal = { tabId: string; assetId: number; assetName: string; assetType: string };

/** 绑定控件:chip 触发的下拉(已打开终端 + 资产库 + 清除 + 联动开关)。绑定到 tab 实例,联动开关闸控双向导航同步。 */
export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const bindTab = useAIStore((s) => s.bindSidebarTab);
  const unbindTab = useAIStore((s) => s.unbindSidebarTab);
  const setSync = useAIStore((s) => s.setSidebarTabSync);
  const assets = useAssetStore((s) => s.assets);
  const tabs = useTabStore((s) => s.tabs);
  const [picking, setPicking] = useState(false);

  // 已打开的工作区 tab → 资产引用,按资产去重(同一资产多开只列第一个 tab)。
  const openTerminals = useMemo(() => {
    const seen = new Set<number>();
    const out: OpenTerminal[] = [];
    for (const wt of tabs) {
      const ref = tabToAssetRef(wt);
      if (!ref || seen.has(ref.assetId)) continue;
      seen.add(ref.assetId);
      out.push({ tabId: wt.id, ...ref });
    }
    return out;
  }, [tabs]);

  if (!sidebarTabId) return null;

  const bound = tab?.linkedAssetId != null;
  const tabLive = tab?.linkedTabId != null && tabs.some((wt) => wt.id === tab.linkedTabId);
  const syncing = !!tab?.syncTab;
  const syncTitle = syncing ? t("ai.sidebar.syncing") : undefined;
  const { Icon: BoundIcon, color: boundColor } = resolveAssetIcon(assets, tab?.linkedAssetId, tab?.linkedAssetType);

  const bindToTerminal = (term: OpenTerminal) =>
    bindTab(sidebarTabId, {
      workspaceTabId: term.tabId,
      assetId: term.assetId,
      assetName: term.assetName,
      assetType: term.assetType,
    });

  // 资产库选择器只回传 id;名称/类型从 assetStore 补全。若该资产恰有打开的 tab 则一并绑 tab,否则仅绑资产(linkedTabId=null)。
  const handleLibraryPick = (assetId: number) => {
    const asset = assets.find((a) => a.ID === assetId);
    if (asset) {
      const open = openTerminals.find((o) => o.assetId === assetId);
      bindTab(sidebarTabId, {
        workspaceTabId: open?.tabId ?? null,
        assetId,
        assetName: asset.Name,
        assetType: asset.Type,
      });
    }
    setPicking(false);
  };

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
                <BoundIcon className="h-3.5 w-3.5" style={boundColor ? { color: boundColor } : undefined} />
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
              {openTerminals.map((term) => {
                const { Icon, color } = resolveAssetIcon(assets, term.assetId, term.assetType);
                const isBound = term.tabId === tab?.linkedTabId;
                return (
                  <DropdownMenuItem
                    key={term.tabId}
                    data-testid={`menu-terminal-${term.assetId}`}
                    onSelect={() => bindToTerminal(term)}
                    className={cn("gap-2", isBound && "bg-primary/10")}
                  >
                    <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                    <Icon className="h-3.5 w-3.5" style={color ? { color } : undefined} />
                    <span className="flex-1 truncate">{term.assetName}</span>
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
