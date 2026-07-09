import type { MentionAttrs } from "./mentionXml";

export function linkedAssetFromMention(m: MentionAttrs): { assetId: number; assetName: string; assetType: string } {
  return { assetId: m.assetId, assetName: m.name, assetType: m.type ?? "" };
}
