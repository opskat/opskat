import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "@opskat/ui";
import { useTabStore } from "@/stores/tabStore";
import "./tabCloseImpact";

/** 关闭 tab 会断开会话 / 中断传输时的确认框。常驻挂载一份，快捷键触发的关闭也不依赖标签栏菜单在不在屏幕上。 */
export function TabCloseConfirmHost() {
  const { t } = useTranslation();
  const closeConfirm = useTabStore((s) => s.closeConfirm);
  const confirmClose = useTabStore((s) => s.confirmClose);
  const cancelCloseConfirm = useTabStore((s) => s.cancelCloseConfirm);

  const descKey =
    closeConfirm && closeConfirm.sessions > 0
      ? closeConfirm.transfers > 0
        ? "tab.closeConfirmDescBoth"
        : "tab.closeConfirmDescSessions"
      : "tab.closeConfirmDescTransfers";

  return (
    <ConfirmDialog
      open={closeConfirm !== null}
      onOpenChange={(open) => {
        if (!open) cancelCloseConfirm();
      }}
      title={t("tab.closeConfirmTitle")}
      description={t(descKey, { sessions: closeConfirm?.sessions ?? 0, transfers: closeConfirm?.transfers ?? 0 })}
      cancelText={t("action.cancel")}
      confirmText={t("tab.closeConfirmAction")}
      onConfirm={confirmClose}
    />
  );
}
