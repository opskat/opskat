import { useMemo } from "react";
import { useTerminalStore, getTerminalActiveAssetIds } from "../stores/terminalStore";
import { useTabStore } from "../stores/tabStore";
import { getQueryActiveAssetIds } from "../stores/queryStore";
import { useRDPStore } from "../stores/rdpStore";

/**
 * Returns the set of asset IDs that are currently "active" across all tab types.
 * - Terminal tabs: asset has at least one connected pane
 * - Query tabs (database/redis): tab is open
 * - RDP tabs: session is connected
 */
export function useActiveAssetIds(): Set<number> {
  // Subscribe to the state slices that affect active IDs
  const tabData = useTerminalStore((s) => s.tabData);
  const tabs = useTabStore((s) => s.tabs);
  const rdpActiveAssetIds = useRDPStore((s) => s.activeAssetIds);

  return useMemo(() => {
    const terminalIds = getTerminalActiveAssetIds();
    const queryIds = getQueryActiveAssetIds();
    return new Set([...terminalIds, ...queryIds, ...rdpActiveAssetIds]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabData, tabs, rdpActiveAssetIds]);
}
