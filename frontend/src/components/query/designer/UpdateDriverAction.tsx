import { useTranslation } from "react-i18next";
import { useDatabaseDriverOwner, type DriverDescriptor } from "@/lib/dbDrivers";
import { openSettingsTab } from "@/stores/tabStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";

/**
 * 「去更新」:驱动由扩展提供时,打开设置里的扩展页(已安装列表里有可用更新的扩展就地显示更新按钮);
 * 内置驱动随宿主更新,没有这个入口。
 */
export function UpdateDriverAction({ descriptor }: { descriptor: DriverDescriptor | undefined }) {
  const { t } = useTranslation();
  const owner = useDatabaseDriverOwner(descriptor?.id);
  if (!owner) return null;
  return (
    <button
      type="button"
      className="shrink-0 font-medium underline-offset-2 hover:underline"
      onClick={() => {
        useSettingsUiStore.getState().setActiveTab("extensions");
        openSettingsTab(t("nav.settings"));
      }}
    >
      {t("query.designer.updateDriver")}
    </button>
  );
}
