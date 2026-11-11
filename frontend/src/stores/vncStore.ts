import { create } from "zustand";

interface VNCState {
  /** Tabs whose VNC session is currently connected (closing them disconnects a session). */
  connectedTabIds: Set<string>;
  setTabConnected: (tabId: string, connected: boolean) => void;
}

export const useVNCStore = create<VNCState>((set) => ({
  connectedTabIds: new Set(),
  setTabConnected: (tabId, connected) =>
    set((state) => {
      if (state.connectedTabIds.has(tabId) === connected) return state;
      const connectedTabIds = new Set(state.connectedTabIds);
      if (connected) connectedTabIds.add(tabId);
      else connectedTabIds.delete(tabId);
      return { connectedTabIds };
    }),
}));
