// Markdown asset reference copied into external agents / chat.
// Format: [Name](opsctl://asset/{id}) — opsctl resolveAsset extracts the numeric id.

import i18n from "@/i18n";
import { notifyCopied } from "@/lib/notify";
import { useAssetStore } from "@/stores/assetStore";

const OPSCTL_ASSET_HREF_RE = /^opsctl:\/\/asset\/(\d+)$/i;
const OPSCTL_ASSET_MARKDOWN_RE = /\[((?:\\.|[^\]])*)\]\((opsctl:\/\/asset\/\d+)\)/gi;
const OPSCTL_ASSET_URI_RE = /opsctl:\/\/asset\/(\d+)/gi;

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

export async function writeAssetMarkdownRef(name: string, id: number): Promise<string> {
  const text = formatAssetMarkdownRef(name, id);
  await navigator.clipboard.writeText(text);
  return text;
}

export async function copyAssetMarkdownRef(name: string, id: number): Promise<string> {
  const text = await writeAssetMarkdownRef(name, id);
  notifyCopied(i18n.t("asset.copyRefCopied"));
  return text;
}

export async function copySelectedAssetMarkdownRef(): Promise<string | null> {
  const { assets, selectedAssetId } = useAssetStore.getState();
  if (selectedAssetId == null) return null;
  const asset = assets.find((item) => item.ID === selectedAssetId);
  if (!asset) return null;
  return copyAssetMarkdownRef(asset.Name, asset.ID);
}

export function shouldCopyAssetRef(target: EventTarget | null): boolean {
  const el = target instanceof HTMLElement ? target : null;
  if (!el) return false;
  if (el.closest(".xterm")) return false;
  if (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable) {
    return false;
  }
  const selection = window.getSelection();
  if (selection && !selection.isCollapsed && (selection.toString() ?? "").length > 0) {
    return false;
  }
  return useAssetStore.getState().selectedAssetId != null;
}
