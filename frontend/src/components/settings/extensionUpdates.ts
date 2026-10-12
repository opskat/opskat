import { useCallback, useEffect } from "react";
import { create } from "zustand";
import { toast } from "sonner";
import i18n from "@/i18n";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { ListStore } from "../../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import type { extstore_svc } from "../../../wailsjs/go/models";

/** Fired by the backend once its daily background refresh updated the store index. */
const STORE_REFRESHED_EVENT = "ext:store-refreshed";

interface ExtensionUpdatesState {
  /** Installed extensions with a compatible newer version in the store, by name: the card to update with. */
  updates: Record<string, extstore_svc.Card>;
  /**
   * Replaces the available updates with those in a store state: the updates the
   * last verified index offers, kept through a failed refresh.
   */
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
      updates: Object.fromEntries(state.updates.map((c) => [c.name, c])),
    }),
}));

/** Re-reads the cached store state (no network) into the updates store. */
export async function syncExtensionUpdates(): Promise<void> {
  useExtensionUpdates.getState().setFromState(await ListStore(i18n.language));
}

/**
 * Fired by the backend whenever the installed set changes (install from any
 * source, uninstall, enable / disable, reload) and once the startup load is done.
 */
const INSTALLED_CHANGED_EVENTS = ["ext:reload", "ext:ready"];

/**
 * Keeps the updates store current while mounted: on mount, after each background
 * refresh, and whenever the installed set changes — an update is relative to
 * what is installed, however it got there.
 */
export function useExtensionUpdateSync() {
  const sync = useCallback(() => {
    syncExtensionUpdates().catch((e) => toast.error(String(e)));
  }, []);
  useEffect(sync, [sync]);
  useWailsEvent(STORE_REFRESHED_EVENT, sync);
  // The extension bundle loader listens to these events too, so unsubscribe with
  // this listener's own cancel: EventsOff(name) — what useWailsEvent does — would
  // drop the loader's listener as well.
  useEffect(() => {
    const offs = INSTALLED_CHANGED_EVENTS.map((event) => EventsOn(event, sync));
    return () => offs.forEach((off) => off?.());
  }, [sync]);
}
