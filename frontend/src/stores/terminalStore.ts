import { create } from "zustand";
import {
  ConnectSSH,
  DisconnectSSH,
} from "../../wailsjs/go/main/App";
import { main } from "../../wailsjs/go/models";

export interface TerminalTab {
  id: string; // sessionId
  assetId: number;
  assetName: string;
  connected: boolean;
}

interface TerminalState {
  tabs: TerminalTab[];
  activeTabId: string | null;

  connect: (
    assetId: number,
    assetName: string,
    password: string,
    cols: number,
    rows: number
  ) => Promise<string>;
  disconnect: (sessionId: string) => void;
  setActiveTab: (id: string | null) => void;
  removeTab: (id: string) => void;
  markClosed: (id: string) => void;
}

export const useTerminalStore = create<TerminalState>((set, get) => ({
  tabs: [],
  activeTabId: null,

  connect: async (assetId, assetName, password, cols, rows) => {
    const req = new main.SSHConnectRequest({
      assetId,
      password,
      key: "",
      cols,
      rows,
    });
    const sessionId = await ConnectSSH(req);

    const tab: TerminalTab = {
      id: sessionId,
      assetId,
      assetName,
      connected: true,
    };
    set((state) => ({
      tabs: [...state.tabs, tab],
      activeTabId: sessionId,
    }));
    return sessionId;
  },

  disconnect: (sessionId) => {
    DisconnectSSH(sessionId);
    set((state) => ({
      tabs: state.tabs.map((t) =>
        t.id === sessionId ? { ...t, connected: false } : t
      ),
    }));
  },

  setActiveTab: (id) => set({ activeTabId: id }),

  removeTab: (id) => {
    const tab = get().tabs.find((t) => t.id === id);
    if (tab?.connected) {
      DisconnectSSH(id);
    }
    set((state) => {
      const tabs = state.tabs.filter((t) => t.id !== id);
      const activeTabId =
        state.activeTabId === id
          ? tabs.length > 0
            ? tabs[tabs.length - 1].id
            : null
          : state.activeTabId;
      return { tabs, activeTabId };
    });
  },

  markClosed: (id) => {
    set((state) => ({
      tabs: state.tabs.map((t) =>
        t.id === id ? { ...t, connected: false } : t
      ),
    }));
  },
}));
