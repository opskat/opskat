import { create } from "zustand";

export type SettingsTab =
  | "ai"
  | "import"
  | "backup"
  | "shortcuts"
  | "terminal"
  | "network"
  | "external-edit"
  | "appearance"
  | "custom-types"
  | "status"
  | "extensions"
  | "about"
  | "debug";

interface SettingsUiState {
  activeTab: SettingsTab;
  setActiveTab: (tab: SettingsTab) => void;
  /** 别的设置页要求打开的扩展详情（扩展名）；扩展页打开它之后清空。 */
  extensionDetail: string | null;
  /** 切到扩展页并打开这个扩展的详情，例如命令审核里"在扩展详情里设置"。 */
  openExtensionDetail: (name: string) => void;
  clearExtensionDetail: () => void;
  /** 别的设置页要求滚动定位的子分区标识（如 "mouse-selection"）；目标区域滚动后清空。 */
  focusSection: string | null;
  /** 切换到指定设置页并定位到页内子分区。 */
  openSettingsSection: (tab: SettingsTab, section?: string) => void;
  clearFocusSection: () => void;
}

export const useSettingsUiStore = create<SettingsUiState>((set) => ({
  activeTab: "ai",
  setActiveTab: (activeTab) => set({ activeTab }),
  extensionDetail: null,
  openExtensionDetail: (name) => set({ activeTab: "extensions", extensionDetail: name }),
  clearExtensionDetail: () => set({ extensionDetail: null }),
  focusSection: null,
  openSettingsSection: (tab, section) => set({ activeTab: tab, focusSection: section ?? null }),
  clearFocusSection: () => set({ focusSection: null }),
}));
