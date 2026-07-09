import type { asset_entity } from "../../wailsjs/go/models";
import type { Tab, PageTabMeta, QueryTabMeta } from "@/stores/tabStore";

/** 从一个工作区 Tab 提取资产引用；非资产 tab（ai/info 或无 assetId）返回 null。 */
export function tabToAssetRef(
  tab: Tab,
  assets: asset_entity.Asset[] = []
): { assetId: number; assetName: string; assetType: string } | null {
  if (tab.type === "ai" || tab.type === "info") return null;
  const meta = tab.meta as { assetId?: number; assetName?: string };
  if (meta == null || typeof meta.assetId !== "number") return null;
  const asset = assets.find((a) => a.ID === meta.assetId);
  if (tab.type === "query") {
    return {
      assetId: meta.assetId,
      assetName: meta.assetName || tab.label || "",
      assetType: (tab.meta as QueryTabMeta).assetType,
    };
  }
  if (tab.type === "page") {
    return {
      assetId: meta.assetId,
      assetName: asset?.Name || tab.label || "",
      assetType: asset?.Type || (tab.meta as PageTabMeta).pageId,
    };
  }
  return { assetId: meta.assetId, assetName: meta.assetName || tab.label || "", assetType: "ssh" };
}
