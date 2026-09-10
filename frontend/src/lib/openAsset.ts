import { toast } from "sonner";
import type { asset_entity } from "../../wailsjs/go/models";
import { useTabStore } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { getAssetType, pageTabPrefix } from "@/lib/assetTypes";

/**
 * 按资产类型打开/聚焦连接 tab。派发只有一处来源——注册表里的 connectAction。
 *
 * 这里曾经有三条并行派发（注册表的 connectAction、一个 `asset.Type === "k8s"` 硬编码
 * 分支、一条扩展分支），于是"这个类型怎么连"的答案分散在三个地方，且互相遮蔽：k8s 的
 * 定义里写着 connectAction: "terminal"，实际却永远走硬编码的 page 分支。
 */
export async function openAssetConnection(asset: asset_entity.Asset): Promise<void> {
  const def = getAssetType(asset.Type);
  if (!def) return;

  if (def.connectAction === "page" && def.pageId) {
    const tabId = `${pageTabPrefix(def)}-${asset.ID}`;
    const tabStore = useTabStore.getState();
    const existing = tabStore.tabs.find((tab) => tab.id === tabId);
    if (existing) {
      tabStore.activateTab(tabId);
      return;
    }
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
    toast.error(`${asset.Name}: ${String(e)}`);
  }
}
