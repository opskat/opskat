import type { Tab } from "@/stores/tabStore";
import type { asset_entity, snippet_entity, snippet_svc } from "../../wailsjs/go/models";

type Asset = asset_entity.Asset;
type Snippet = snippet_entity.Snippet;
type Category = snippet_svc.Category;

/**
 * Where a snippet chosen from the command palette should run:
 * - "active": the currently focused tab already targets a matching asset, so
 *   insert straight into it (no host picker).
 * - "pick": open the asset-picker drawer to choose host(s).
 */
export type SnippetTarget = { kind: "active"; asset: Asset } | { kind: "pick" };

/** Asset type implied by the active tab's tool, or null if it targets no asset. */
function activeTabAsset(tab: Tab): { assetType: string; assetId: number } | null {
  if (tab.meta.type === "terminal") return { assetType: "ssh", assetId: tab.meta.assetId };
  if (tab.meta.type === "query") return { assetType: tab.meta.assetType, assetId: tab.meta.assetId };
  return null;
}

/**
 * Decide the target for running a snippet from the command palette.
 * "active tab first, else pick host": if the focused tab matches the snippet's
 * category asset type and that asset is known, run there; otherwise pick a host.
 * Pure — never throws.
 */
export function resolveSnippetTarget(params: {
  snippet: Snippet;
  activeTab: Tab | undefined;
  assetsById: Map<number, Asset>;
  categories: Category[];
}): SnippetTarget {
  const { snippet, activeTab, assetsById, categories } = params;

  const assetType = categories.find((c) => c.id === snippet.Category)?.assetType ?? "";
  if (!assetType || !activeTab) return { kind: "pick" };

  const active = activeTabAsset(activeTab);
  if (!active || active.assetType !== assetType) return { kind: "pick" };

  const asset = assetsById.get(active.assetId);
  if (!asset) return { kind: "pick" };

  return { kind: "active", asset };
}
