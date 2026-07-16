import type { asset_entity } from "../../../wailsjs/go/models";
import { getSnippetRunner } from "@/lib/snippetRunners";

/**
 * Whether a snippet category can be run, given the current category registry.
 * A category is runnable when its bound asset type has a registered runner.
 */
export function isRunnableCategoryId(categoryId: string, categories: { id: string; assetType: string }[]): boolean {
  const c = categories.find((x) => x.id === categoryId);
  return !!c && getSnippetRunner(c.assetType) !== undefined;
}

/**
 * Open the right tab for an asset and land the snippet content in its editor.
 * Never auto-executes.
 */
export async function runSnippetOnAsset(asset: asset_entity.Asset, content: string): Promise<void> {
  const runner = getSnippetRunner(asset.Type);
  if (!runner) throw new Error(`snippetRun: unsupported asset type ${asset.Type}`);
  await runner(asset, content);
}
