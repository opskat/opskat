import { useCallback, useEffect } from "react";
import { create } from "zustand";
import { toast } from "sonner";
import i18n from "@/i18n";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { ListStore } from "../../../wailsjs/go/extension/Extension";
import type { extstore_svc } from "../../../wailsjs/go/models";

/** Fired by the backend once its daily background refresh updated the store index. */
const STORE_REFRESHED_EVENT = "ext:store-refreshed";

interface ExtensionUpdatesState {
  /** Installed extensions with a compatible newer version in the store, by name: the card to update with. */
  updates: Record<string, extstore_svc.Card>;
  /** Replaces the available updates with those in a store state. */
  setFromState: (state: extstore_svc.State) => void;
}

/**
 * The one place that knows which installed extensions can be updated. Whoever
 * obtains a store state (the store page, a refresh, an install, the startup
 * event) publishes it here; the Installed list and the tab badge only read.
 */
export const useExtensionUpdates = create<ExtensionUpdatesState>((set) => ({
  updates: {},
  setFromState: (state) =>
    set({
      updates: Object.fromEntries(state.extensions.filter((c) => c.action === "update").map((c) => [c.name, c])),
    }),
}));

/** Re-reads the cached store state (no network) into the updates store. */
export async function syncExtensionUpdates(): Promise<void> {
  useExtensionUpdates.getState().setFromState(await ListStore(i18n.language));
}

/** Keeps the updates store current while mounted: on mount and after each background refresh. */
export function useExtensionUpdateSync() {
  const sync = useCallback(() => {
    syncExtensionUpdates().catch((e) => toast.error(String(e)));
  }, []);
  useEffect(sync, [sync]);
  useWailsEvent(STORE_REFRESHED_EVENT, sync);
}
