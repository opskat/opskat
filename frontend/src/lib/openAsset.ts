import { toast } from "sonner";
import type { asset_entity } from "../../wailsjs/go/models";
import { useTabStore, type PageTabMeta, type QueryTabMeta, type Tab, type TerminalTabMeta } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { getAssetType, pageTabPrefix } from "@/lib/assetTypes";
import { errorMessage } from "@/lib/errorMessage";
import { pickExistingAssetTab, pickReconnectTargetTab, reconnectTab, reconnectTabIfNeeded } from "@/lib/tabReconnect";

/**
 * 查找该资产已打开的全部连接标签页（仅按注册表的 connectAction 与 tab.meta 匹配，不在共享层分支资产类型）。
 * page 类型按 pageId + assetId + extensionName 匹配，同时覆盖单标签与多标签（如 vnc-${id}-${ts}）。
 */
export function findExistingAssetTabs(asset: Pick<asset_entity.Asset, "ID" | "Type">, tabs: Tab[]): Tab[] {
  const def = getAssetType(asset.Type);
  if (!def) return [];

  if (def.connectAction === "page" && def.pageId) {
    return tabs.filter((tab) => {
      if (tab.type !== "page") return false;
      const meta = tab.meta as PageTabMeta;
      return (
        meta.pageId === def.pageId &&
        meta.assetId === asset.ID &&
        (meta.extensionName ?? undefined) === (def.extensionName ?? undefined)
      );
    });
  }

  if (def.connectAction === "query") {
    return tabs.filter((tab) => tab.type === "query" && (tab.meta as QueryTabMeta).assetId === asset.ID);
  }

  if (def.connectAction === "terminal") {
    return tabs.filter((tab) => tab.type === "terminal" && (tab.meta as TerminalTabMeta).assetId === asset.ID);
  }

  return [];
}

/**
 * 按资产类型打开/聚焦连接 tab。派发只有一处来源——注册表里的 connectAction。
 * 已有标签时：优先激活匹配标签；若其处于失败 / 取消 / 已断开态则直接重连，
 * 连接中 / 等待用户输入 / 已连接态仅切到该标签而不中断（#380）。
 */
export async function openAssetConnection(asset: asset_entity.Asset): Promise<void> {
  const def = getAssetType(asset.Type);
  if (!def) return;

  const tabStore = useTabStore.getState();
  const existingTabs = findExistingAssetTabs(asset, tabStore.tabs);
  if (existingTabs.length > 0) {
    const target = pickExistingAssetTab(existingTabs, tabStore.activeTabId ?? null)!;
    tabStore.activateTab(target.id);
    try {
      await reconnectTabIfNeeded(target);
    } catch (e) {
      toast.error(`${asset.Name}: ${errorMessage(e)}`);
    }
    return;
  }

  if (def.connectAction === "page" && def.pageId) {
    const tabId = `${pageTabPrefix(def)}-${asset.ID}`;
    tabStore.openTab({
      id: tabId,
      type: "page",
      label: asset.Name,
      icon: asset.Icon || def.pageIcon,
      meta: {
        type: "page",
        pageId: def.pageId,
        assetId: asset.ID,
        ...(def.extensionName ? { extensionName: def.extensionName } : {}),
      },
    });
    return;
  }

  if (def.connectAction === "query") {
    useQueryStore.getState().openQueryTab(asset);
    return;
  }

  if (def.connectAction !== "terminal") return;
  try {
    await useTerminalStore.getState().connect(asset);
  } catch (e) {
    toast.error(`${asset.Name}: ${errorMessage(e)}`);
  }
}

/**
 * 显式重新连接资产（侧栏右键「重新连接」）：
 * 已有标签时无视当前状态（包括连接中）立即激活并触发重连；尚无标签时按普通打开流程建立连接。
 */
export async function reconnectAssetConnection(asset: asset_entity.Asset): Promise<void> {
  const def = getAssetType(asset.Type);
  if (!def) return;

  const tabStore = useTabStore.getState();
  const existingTabs = findExistingAssetTabs(asset, tabStore.tabs);
  if (existingTabs.length === 0) {
    await openAssetConnection(asset);
    return;
  }

  const target = pickReconnectTargetTab(existingTabs, tabStore.activeTabId ?? null)!;
  tabStore.activateTab(target.id);
  try {
    await reconnectTab(target);
  } catch (e) {
    toast.error(`${asset.Name}: ${errorMessage(e)}`);
  }
}
