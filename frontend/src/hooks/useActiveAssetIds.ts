import { useMemo } from "react";
import { useTerminalStore, getTerminalActiveAssetIds } from "../stores/terminalStore";
import { useTabStore } from "../stores/tabStore";
import { getQueryActiveAssetIds } from "../stores/queryStore";
import { useRDPStore } from "../stores/rdpStore";

/** Terminal + query active IDs as a sorted key, so subscribers re-render only when the set changes. */
function tabActiveIdsKey(): string {
  const ids = [...getTerminalActiveAssetIds(), ...getQueryActiveAssetIds()];
  return ids.sort((a, b) => a - b).join(",");
}

/**
 * Returns the set of asset IDs that are currently "active" across all tab types.
 * - Terminal tabs: asset has at least one connected pane
 * - Query tabs (database/redis): tab is open
 * - RDP tabs: session is connected
 *
 * The returned Set keeps its identity until its contents change (e.g. a terminal title or
 * pane update that does not connect/disconnect anything returns the same Set).
 */
export function useActiveAssetIds(): Set<number> {
  // The key reads both stores; subscribe to each so a change in either re-evaluates it.
  const terminalKey = useTerminalStore(tabActiveIdsKey);
  const tabsKey = useTabStore(tabActiveIdsKey);
  const rdpActiveAssetIds = useRDPStore((s) => s.activeAssetIds);

  return useMemo(() => {
    return new Set([...getTerminalActiveAssetIds(), ...getQueryActiveAssetIds(), ...rdpActiveAssetIds]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [terminalKey, tabsKey, rdpActiveAssetIds]);
}
