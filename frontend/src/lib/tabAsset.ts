import type { Tab, QueryTabMeta } from "@/stores/tabStore";

/** 从一个工作区 Tab 提取资产引用；非资产 tab（ai/page/info 或无 assetId）返回 null。 */
export function tabToAssetRef(tab: Tab): { assetId: number; assetName: string; assetType: string } | null {
  if (tab.type === "ai" || tab.type === "page" || tab.type === "info") return null;
  const meta = tab.meta as { assetId?: number; assetName?: string };
  if (meta == null || typeof meta.assetId !== "number") return null;
  const assetType = tab.type === "query" ? (tab.meta as QueryTabMeta).assetType : "ssh";
  return { assetId: meta.assetId, assetName: meta.assetName || tab.label || "", assetType };
}
