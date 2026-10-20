import { create } from "zustand";

export type SettingsTab =
  | "ai"
  | "import"
  | "backup"
  | "shortcuts"
  | "terminal"
  | "external-edit"
  | "appearance"
  | "status"
  | "extensions"
  | "about";

interface SettingsUiState {
  activeTab: SettingsTab;
  setActiveTab: (tab: SettingsTab) => void;
  /** 别的设置页要求打开的扩展详情（扩展名）；扩展页打开它之后清空。 */
  extensionDetail: string | null;
  /** 切到扩展页并打开这个扩展的详情，例如命令审核里"去扩展里设置"。 */
  openExtensionDetail: (name: string) => void;
  clearExtensionDetail: () => void;
}

export const useSettingsUiStore = create<SettingsUiState>((set) => ({
  activeTab: "ai",
  setActiveTab: (activeTab) => set({ activeTab }),
  extensionDetail: null,
  openExtensionDetail: (name) => set({ activeTab: "extensions", extensionDetail: name }),
  clearExtensionDetail: () => set({ extensionDetail: null }),
}));
