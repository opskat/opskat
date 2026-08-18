import { buildGroupPathMap } from "@/lib/groupPath";
import { splitOpsctlAssetRefs } from "@/lib/assetRef";
import { buildMentionXml, escapeXmlText, parseMentionContent } from "@/lib/mentionXml";
import { useAssetStore } from "@/stores/assetStore";
import type {
  AIChatInputDraft,
  ProseMirrorLikeNode,
  TipTapDocNode,
  TipTapMentionNode,
  TipTapParagraphNode,
  TipTapTextNode,
} from "./types";

export function extractContentXml(doc: ProseMirrorLikeNode): string {
  const assetStore = useAssetStore.getState();
  const groupPathMap = buildGroupPathMap(assetStore.groups);
  const lookupAsset = (id: number) => assetStore.assets.find((asset) => asset.ID === id);
  const hostFromConfig = (cfg: string | undefined) => {
    if (!cfg) return undefined;
    try {
      const parsed = JSON.parse(cfg) as { host?: string };
      return parsed.host || undefined;
    } catch {
      return undefined;
    }
  };
  const driverFromConfig = (cfg: string | undefined) => {
    if (!cfg) return undefined;
    try {
      const parsed = JSON.parse(cfg) as { driver?: string };
      return parsed.driver || undefined;
    } catch {
      return undefined;
    }
  };
  const stringAttr = (value: unknown) => (typeof value === "string" && value ? value : undefined);
  const mentionTarget = (value: unknown) =>
    value === "database" || value === "table" || value === "asset" ? value : undefined;

  let out = "";
  doc.descendants((node) => {
    if (node.type.name === "text") {
      for (const part of splitOpsctlAssetRefs(node.text ?? "")) {
        if (part.type === "text") {
          out += escapeXmlText(part.text);
          continue;
        }
        const asset = lookupAsset(part.id);
        out += buildMentionXml({
          assetId: part.id,
          name: asset?.Name || part.name,
          type: asset?.Type,
          host: asset ? hostFromConfig(asset.Config) : undefined,
          groupPath: asset?.GroupID ? groupPathMap.get(asset.GroupID) : undefined,
          driver: asset ? driverFromConfig(asset.Config) : undefined,
        });
      }
    } else if (node.type.name === "hardBreak") {
      out += "\n";
    } else if (node.type.name === "mention") {
      const id = Number(node.attrs.id);
      const label = String(node.attrs.label ?? "");
      const asset = Number.isFinite(id) ? lookupAsset(id) : undefined;
      const target = mentionTarget(node.attrs.kind);
      out += buildMentionXml({
        assetId: id,
        name: label,
        type: asset?.Type,
        host: asset ? hostFromConfig(asset.Config) : undefined,
        groupPath: asset?.GroupID ? groupPathMap.get(asset.GroupID) : undefined,
        target,
        database: stringAttr(node.attrs.database),
        table: stringAttr(node.attrs.table),
        driver: stringAttr(node.attrs.driver) ?? (asset ? driverFromConfig(asset.Config) : undefined),
      });
    } else if (node.type.name === "paragraph" && out.length > 0) {
      out += "\n";
    }
    return true;
  });
  return out.replace(/\n+$/g, "");
}

function normalizeDraftMessage(draft: string | AIChatInputDraft): AIChatInputDraft {
  if (typeof draft === "string") {
    return { content: draft };
  }
  return { content: draft.content ?? "" };
}

function appendTextToParagraphs(
  paragraphs: TipTapParagraphNode[],
  text: string,
  currentParagraphContent: Array<TipTapTextNode | TipTapMentionNode>
) {
  const segments = text.split("\n");
  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index];
    if (segment.length > 0) {
      currentParagraphContent.push({ type: "text", text: segment });
    }
    if (index < segments.length - 1) {
      paragraphs.push(
        currentParagraphContent.length > 0
          ? { type: "paragraph", content: currentParagraphContent }
          : { type: "paragraph" }
      );
      currentParagraphContent = [];
    }
  }
  return currentParagraphContent;
}

export function buildPastedOpsctlContent(text: string): Array<TipTapTextNode | TipTapMentionNode> | null {
  const parts = splitOpsctlAssetRefs(text);
  if (!parts.some((part) => part.type === "ref")) return null;

  const assetStore = useAssetStore.getState();
  const content: Array<TipTapTextNode | TipTapMentionNode> = [];
  for (const part of parts) {
    if (part.type === "text") {
      if (part.text) content.push({ type: "text", text: part.text });
      continue;
    }
    const asset = assetStore.assets.find((item) => item.ID === part.id);
    content.push({
      type: "mention",
      attrs: {
        id: String(part.id),
        label: asset?.Name || part.name,
      },
    });
  }
  return content;
}

export function buildEditorDocFromMessage(message: string | AIChatInputDraft): TipTapDocNode {
  const { content } = normalizeDraftMessage(message);
  const segments = parseMentionContent(content);
  const paragraphs: TipTapParagraphNode[] = [];
  let currentParagraphContent: Array<TipTapTextNode | TipTapMentionNode> = [];

  for (const seg of segments) {
    if (seg.type === "text") {
      currentParagraphContent = appendTextToParagraphs(paragraphs, seg.text, currentParagraphContent);
    } else {
      currentParagraphContent.push({
        type: "mention",
        attrs: {
          id: String(seg.attrs.assetId),
          label: seg.attrs.name,
          kind: seg.attrs.target,
          database: seg.attrs.database,
          table: seg.attrs.table,
          driver: seg.attrs.driver,
        },
      });
    }
  }

  paragraphs.push(
    currentParagraphContent.length > 0 ? { type: "paragraph", content: currentParagraphContent } : { type: "paragraph" }
  );

  return {
    type: "doc",
    content: paragraphs,
  };
}
