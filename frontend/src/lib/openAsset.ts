import { toast } from "sonner";
import type { asset_entity } from "../../wailsjs/go/models";
import { useTabStore } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { useExtensionStore } from "@/extension";
import { useTerminalStore } from "@/stores/terminalStore";
import { getAssetType } from "@/lib/assetTypes";

/** 按资产类型打开/聚焦连接 tab（k8s/扩展→page；query→查询 tab；terminal→连接）。与资产列表双击行为一致。 */
export async function openAssetConnection(asset: asset_entity.Asset): Promise<void> {
  const def = getAssetType(asset.Type);
  if (def?.connectAction === "page" && def.pageId) {
    const pageId = `${def.pageId}-${asset.ID}`;
    const tabStore = useTabStore.getState();
    const existing = tabStore.tabs.find((tab) => tab.id === pageId);
    if (existing) {
      tabStore.activateTab(pageId);
    } else {
      tabStore.openTab({
        id: pageId,
        type: "page",
        label: asset.Name,
        icon: asset.Icon || def.pageIcon,
        meta: { type: "page", pageId: def.pageId, assetId: asset.ID },
      });
    }
    return;
  }
  if (asset.Type === "k8s") {
    const pageId = `k8s-${asset.ID}`;
    const tabStore = useTabStore.getState();
    const existing = tabStore.tabs.find((t) => t.id === pageId);
    if (existing) {
      tabStore.activateTab(pageId);
    } else {
      tabStore.openTab({
        id: pageId,
        type: "page",
        label: asset.Name,
        icon: asset.Icon || "kubernetes",
        meta: { type: "page", pageId: "k8s-cluster", assetId: asset.ID },
      });
    }
    return;
  }
  if (def?.connectAction === "query") {
    useQueryStore.getState().openQueryTab(asset);
    return;
  }

  // Check if this is an extension asset type
  const ext = useExtensionStore.getState().getExtensionForAssetType(asset.Type);
  if (ext) {
    const connectPage = ext.manifest.frontend?.pages.find((p) => p.slot === "asset.connect");
    if (connectPage) {
      useTabStore.getState().openTab({
        id: `ext-${asset.ID}-${connectPage.id}`,
        type: "page",
        label: asset.Name,
        icon: ext.manifest.icon,
        meta: {
          type: "page",
          pageId: connectPage.id,
          extensionName: ext.name,
          assetId: asset.ID,
        },
      });
      return;
    }
  }

  if (def?.connectAction !== "terminal") return;
  try {
    await useTerminalStore.getState().connect(asset);
  } catch (e) {
    toast.error(`${asset.Name}: ${String(e)}`);
  }
}
