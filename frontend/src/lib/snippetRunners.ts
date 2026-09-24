import type { asset_entity } from "../../wailsjs/go/models";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore, type TerminalTabMeta } from "@/stores/tabStore";
import { TRANSPORTS, useTerminalStore, type TerminalTabData, type TerminalTransport } from "@/stores/terminalStore";
import { bytesToBase64 } from "@/lib/terminalEncode";

export type SnippetRunner = (asset: asset_entity.Asset, content: string) => Promise<void> | void;

interface SnippetRunnerRegistration {
  runner: SnippetRunner;
  assetTypes: readonly string[];
}

const runners = new Map<string, SnippetRunnerRegistration>();

export function registerSnippetRunner(assetType: string, runner: SnippetRunner, aliases: readonly string[] = []): void {
  const registration = { runner, assetTypes: [...new Set([assetType, ...aliases])] };
  for (const type of registration.assetTypes) runners.set(type, registration);
}

export function getSnippetRunner(assetType: string): SnippetRunner | undefined {
  return runners.get(assetType)?.runner;
}

export function getSnippetRunnerAssetTypes(assetType: string): readonly string[] {
  return runners.get(assetType)?.assetTypes ?? [];
}

export function isSnippetRunnerCompatible(leftAssetType: string, rightAssetType: string): boolean {
  const runner = runners.get(leftAssetType);
  return runner !== undefined && runner === runners.get(rightAssetType);
}

const terminalSnippetRunner: SnippetRunner = async (asset, content) => {
  const existing = findExistingConnectedPane(asset.ID);
  if (existing) {
    await TRANSPORTS[existing.transport].write(existing.paneId, bytesToBase64(new TextEncoder().encode(content)));
    return;
  }
  await useTerminalStore.getState().connect(asset, "", false, { initialInput: content });
};

registerSnippetRunner("ssh", terminalSnippetRunner, ["local"]);

registerSnippetRunner("database", (asset, content) => {
  useQueryStore.getState().openQueryTab(asset, { initialSQL: content });
});

registerSnippetRunner("mongodb", (asset, content) => {
  useQueryStore.getState().openQueryTab(asset, { initialMongo: content });
});

function findExistingConnectedPane(assetId: number): { paneId: string; transport: TerminalTransport } | null {
  const { tabData } = useTerminalStore.getState();
  const tabs = useTabStore.getState().tabs;

  for (const tab of tabs) {
    if (tab.type !== "terminal") continue;
    const meta = tab.meta as TerminalTabMeta;
    if (meta.assetId !== assetId) continue;

    const data: TerminalTabData | undefined = tabData[tab.id];
    if (!data) continue;

    const paneId = data.activePaneId;
    const pane = data.panes[paneId];
    if (paneId && pane?.connected) {
      return { paneId, transport: pane.transport };
    }
  }
  return null;
}
