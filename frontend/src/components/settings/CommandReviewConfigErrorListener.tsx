import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { openSettingsTab } from "@/stores/tabStore";

// 后端（internal/app/system/command_review.go）只对这几种配置错误发事件，每种只发一次。
const CONFIG_ERRORS = new Set(["not_configured", "api_key_unreadable", "invalid_api_key"]);

/** 命令审核遇到配置错误时提醒一次，提示里能直接打开设置 › AI。挂在 App 里，不渲染内容。 */
export function CommandReviewConfigErrorListener() {
  const { t } = useTranslation();

  useWailsEvent(
    "command-review:config-error",
    useCallback(
      (reason: string) => {
        if (!CONFIG_ERRORS.has(reason)) return;
        toast.warning(t(`commandReview.configError.${reason}`), {
          action: {
            label: t("commandReview.configError.openSettings"),
            onClick: () => {
              useSettingsUiStore.getState().setActiveTab("ai");
              openSettingsTab(t("nav.settings"));
            },
          },
        });
      },
      [t]
    )
  );

  return null;
}
