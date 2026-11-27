// Markdown asset reference copied into external agents / chat.
// Format: [Name](opsctl://asset/{id}) — opsctl resolveAsset extracts the numeric id.

import i18n from "@/i18n";
import { notifyCopied } from "@/lib/notify";
import { buildTreeIndex, flattenTree } from "@/lib/assetTreeDnd";
import { useAssetStore } from "@/stores/assetStore";

const OPSCTL_ASSET_HREF_RE = /^opsctl:\/\/asset\/(\d+)$/i;
const OPSCTL_ASSET_MARKDOWN_RE = /\[((?:\\.|[^\]])*)\]\((opsctl:\/\/asset\/\d+)\)/gi;
const OPSCTL_ASSET_URI_RE = /opsctl:\/\/asset\/(\d+)/gi;

/**
 * Marks the asset sidebar root. The asset-reference shortcut is scoped to this
 * surface: see {@link shouldCopyAssetRef}.
 */
export const ASSET_SIDEBAR_ATTR = "data-asset-sidebar";

export function formatAssetMarkdownRef(name: string, id: number): string {
  const escaped = name.replace(/\\/g, "\\\\").replace(/\[/g, "\\[").replace(/\]/g, "\\]");
  return `[${escaped}](opsctl://asset/${id})`;
}

export function parseOpsctlAssetHref(href: string | null | undefined): number | null {
  if (!href) return null;
  const match = href.trim().match(OPSCTL_ASSET_HREF_RE);
  if (!match) return null;
  const id = Number.parseInt(match[1], 10);
  return Number.isFinite(id) ? id : null;
}

function unescapeMarkdownLinkText(text: string): string {
  return text.replace(/\\(\\|\[|\])/g, "$1");
}

export function parseOpsctlAssetMarkdown(text: string): { name: string; id: number } | null {
  const match = /^\[((?:\\.|[^\]])*)\]\((opsctl:\/\/asset\/\d+)\)$/i.exec(text.trim());
  if (!match) return null;
  const id = parseOpsctlAssetHref(match[2]);
  if (id == null) return null;
  return { name: unescapeMarkdownLinkText(match[1]), id };
}

export type OpsctlAssetRefPart = { type: "text"; text: string } | { type: "ref"; name: string; id: number };

export function splitOpsctlAssetRefs(text: string): OpsctlAssetRefPart[] {
  if (!text) return [];
  const parts: OpsctlAssetRefPart[] = [];
  const covered = new Array<boolean>(text.length).fill(false);
  const refs: Array<{ start: number; end: number; name: string; id: number }> = [];

  const markdownRe = new RegExp(OPSCTL_ASSET_MARKDOWN_RE.source, "gi");
  let match: RegExpExecArray | null;
  while ((match = markdownRe.exec(text)) !== null) {
    const id = parseOpsctlAssetHref(match[2]);
    if (id == null) continue;
    refs.push({
      start: match.index,
      end: match.index + match[0].length,
      name: unescapeMarkdownLinkText(match[1]),
      id,
    });
    covered.fill(true, match.index, match.index + match[0].length);
  }

  const uriRe = new RegExp(OPSCTL_ASSET_URI_RE.source, "gi");
  while ((match = uriRe.exec(text)) !== null) {
    if (covered[match.index]) continue;
    const id = Number.parseInt(match[1], 10);
    if (!Number.isFinite(id)) continue;
    refs.push({
      start: match.index,
      end: match.index + match[0].length,
      name: String(id),
      id,
    });
  }

  refs.sort((a, b) => a.start - b.start);
  let last = 0;
  for (const ref of refs) {
    if (ref.start < last) continue;
    if (ref.start > last) parts.push({ type: "text", text: text.slice(last, ref.start) });
    parts.push({ type: "ref", name: ref.name, id: ref.id });
    last = ref.end;
  }
  if (last < text.length) parts.push({ type: "text", text: text.slice(last) });
  return parts.length > 0 ? parts : [{ type: "text", text }];
}

export function formatAssetMarkdownRefs(assets: ReadonlyArray<{ Name: string; ID: number }>): string {
  return assets.map((asset) => formatAssetMarkdownRef(asset.Name, asset.ID)).join("\n");
}

export async function writeAssetMarkdownRef(name: string, id: number): Promise<string> {
  const text = formatAssetMarkdownRef(name, id);
  await navigator.clipboard.writeText(text);
  return text;
}

export async function writeAssetMarkdownRefs(assets: ReadonlyArray<{ Name: string; ID: number }>): Promise<string> {
  const text = formatAssetMarkdownRefs(assets);
  await navigator.clipboard.writeText(text);
  return text;
}

export async function copyAssetMarkdownRef(name: string, id: number): Promise<string> {
  const text = await writeAssetMarkdownRef(name, id);
  notifyCopied(i18n.t("asset.copyRefCopied"));
  return text;
}

export async function copyAssetMarkdownRefs(
  assets: ReadonlyArray<{ Name: string; ID: number }>
): Promise<string | null> {
  if (assets.length === 0) return null;
  if (assets.length === 1) return copyAssetMarkdownRef(assets[0].Name, assets[0].ID);
  const text = await writeAssetMarkdownRefs(assets);
  notifyCopied(i18n.t("asset.copyRefsCopied", { count: assets.length }));
  return text;
}

export async function copySelectedAssetMarkdownRef(): Promise<string | null> {
  const { assets, groups, collapsedGroupIds, selectedAssetId, selectedAssetIds } = useAssetStore.getState();
  const targetIds = selectedAssetIds.length > 0 ? selectedAssetIds : selectedAssetId != null ? [selectedAssetId] : [];
  if (targetIds.length === 0) return null;

  const targetSet = new Set(targetIds);
  const assetById = new Map(assets.map((item) => [item.ID, item] as const));

  // 多选时按当前可见树顺序排列，确保复制结果与列表顺序一致；不在可见树中的项按 targetIds 顺序兜底。
  const ordered: Array<{ Name: string; ID: number }> = [];
  const added = new Set<number>();
  if (targetIds.length > 1) {
    const rows = flattenTree({
      index: buildTreeIndex(groups, assets),
      collapsedGroupIDs: new Set(collapsedGroupIds),
      shouldHideEmpty: false,
    });
    for (const row of rows) {
      if (row.kind === "asset" && targetSet.has(row.assetID) && !added.has(row.assetID)) {
        const asset = assetById.get(row.assetID);
        if (asset) {
          ordered.push(asset);
          added.add(row.assetID);
        }
      }
    }
  }
  for (const id of targetIds) {
    if (added.has(id)) continue;
    const asset = assetById.get(id);
    if (asset) {
      ordered.push(asset);
      added.add(id);
    }
  }
  return copyAssetMarkdownRefs(ordered);
}

export function shouldCopyAssetRef(target: EventTarget | null): boolean {
  const el = target instanceof HTMLElement ? target : null;
  if (!el) return false;
  // Only the focused asset sidebar owns this shortcut. A keydown targets the focused
  // element, so containment here means "the sidebar is the surface the user is acting on"
  // — every other surface (grid, terminal, SFTP list, OSS list) keeps the copy key.
  if (!el.closest(`[${ASSET_SIDEBAR_ATTR}]`)) return false;
  if (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable) {
    return false;
  }
  const selection = window.getSelection();
  if (selection && !selection.isCollapsed && (selection.toString() ?? "").length > 0) {
    return false;
  }
  const { selectedAssetId, selectedAssetIds } = useAssetStore.getState();
  return selectedAssetIds.length > 0 || selectedAssetId != null;
}
