import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

/** 打开资产 / 分组的信息标签页；已经开着就切过去。 */
export function openInfoTab(type: "asset" | "group", id: number, name: string, icon?: string) {
  const tabStore = useTabStore.getState();
  const infoTabId = `info-${type}-${id}`;
  if (tabStore.tabs.some((t) => t.id === infoTabId)) {
    tabStore.activateTab(infoTabId);
    return;
  }
  tabStore.openTab({
    id: infoTabId,
    type: "info",
    label: name,
    icon,
    meta: { type: "info", targetType: type, targetId: id, name, icon },
  });
}

/** 选中分组并打开它的详情，与资产树上的"分组详情"相同。 */
export function openGroupDetail(group: { ID: number; Name: string; Icon?: string }) {
  const assets = useAssetStore.getState();
  assets.selectGroup(group.ID);
  assets.selectAsset(null);
  openInfoTab("group", group.ID, group.Name, group.Icon || undefined);
}
