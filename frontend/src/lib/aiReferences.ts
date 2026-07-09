import type { MentionAttrs } from "./mentionXml";
import { extractMentions } from "./mentionXml";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { openAssetConnection } from "./openAsset";
import { tabToAssetRef } from "./tabAsset";
import { toast } from "sonner";
import i18n from "../i18n";

/** 从会话消息里汇总被 @mention 的资产（去重保序，可排除已绑定主资产）。 */
export function deriveReferences(
  messages: { role: string; content: string }[],
  opts?: { excludeAssetId?: number | null }
): MentionAttrs[] {
  const seen = new Set<number>();
  const out: MentionAttrs[] = [];
  for (const m of messages) {
    if (m.role !== "user") continue;
    for (const mention of extractMentions(m.content)) {
      if (mention.assetId === opts?.excludeAssetId) continue;
      if (seen.has(mention.assetId)) continue;
      seen.add(mention.assetId);
      out.push(mention);
    }
  }
  return out;
}

/** 该资产是否已有打开的工作区 tab（决定 ↗ 复用 / + 新开）。 */
export function isAssetTabOpen(assetId: number): boolean {
  const assets = useAssetStore.getState().assets;
  return useTabStore.getState().tabs.some((tab) => tabToAssetRef(tab, assets)?.assetId === assetId);
}

/** 跳转到资产：已开 tab 则聚焦；否则打开新连接。 */
export async function jumpToAsset(assetId: number): Promise<void> {
  const assets = useAssetStore.getState().assets;
  const open = useTabStore.getState().tabs.find((tab) => tabToAssetRef(tab, assets)?.assetId === assetId);
  if (open) {
    useTabStore.getState().activateTab(open.id);
    return;
  }
  const asset = useAssetStore.getState().assets.find((a) => a.ID === assetId);
  if (!asset) {
    toast.error(i18n.t("ai.mentionAssetDeleted"));
    return;
  }
  await openAssetConnection(asset);
}
