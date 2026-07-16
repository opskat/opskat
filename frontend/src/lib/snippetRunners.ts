import type { asset_entity } from "../../wailsjs/go/models";
import { WriteSSH } from "../../wailsjs/go/ssh/SSH";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore, type TerminalTabMeta } from "@/stores/tabStore";
import { useTerminalStore, type TerminalTabData } from "@/stores/terminalStore";
import { bytesToBase64 } from "@/lib/terminalEncode";

export type SnippetRunner = (asset: asset_entity.Asset, content: string) => Promise<void> | void;

const runners = new Map<string, SnippetRunner>();

export function registerSnippetRunner(assetType: string, runner: SnippetRunner): void {
  runners.set(assetType, runner);
}

export function getSnippetRunner(assetType: string): SnippetRunner | undefined {
  return runners.get(assetType);
}

registerSnippetRunner("ssh", async (asset, content) => {
  const existing = findExistingConnectedPane(asset.ID);
  if (existing) {
    await WriteSSH(existing.paneId, bytesToBase64(new TextEncoder().encode(content)));
    return;
  }
  await useTerminalStore.getState().connect(asset, "", false, { initialInput: content });
});

registerSnippetRunner("database", (asset, content) => {
  useQueryStore.getState().openQueryTab(asset, { initialSQL: content });
});

registerSnippetRunner("mongodb", (asset, content) => {
  useQueryStore.getState().openQueryTab(asset, { initialMongo: content });
});

function findExistingConnectedPane(assetId: number): { paneId: string } | null {
  const { tabData } = useTerminalStore.getState();
  const tabs = useTabStore.getState().tabs;

  for (const tab of tabs) {
    if (tab.type !== "terminal") continue;
    const meta = tab.meta as TerminalTabMeta;
    if (meta.assetId !== assetId) continue;

    const data: TerminalTabData | undefined = tabData[tab.id];
    if (!data) continue;

    const paneId = data.activePaneId;
    if (paneId && data.panes[paneId]?.connected) {
      return { paneId };
    }
  }
  return null;
}
