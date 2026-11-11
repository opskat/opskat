import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useShallow } from "zustand/shallow";
import { getConversationDisplayTitle, selectWaitingApprovalConversationIds, useAIStore } from "@/stores/aiStore";

const toastIdOf = (convId: number) => `ai-approval-${convId}`;

/**
 * 会话等待审批且 AI 面板折叠时弹一条带「查看」的警告提示（每次等待只弹一次；
 * 面板展开时开始等待、之后才折叠的也会弹），审批处理完后撤掉。顶栏数量由 TopBar 自行订阅。
 */
export function useApprovalNotice(panelCollapsed: boolean, expandPanel: () => void) {
  const { t } = useTranslation();
  const waitingIds = useAIStore(useShallow(selectWaitingApprovalConversationIds));
  const notified = useRef(new Set<number>());

  useEffect(() => {
    const waiting = new Set(waitingIds);
    for (const id of [...notified.current]) {
      if (!waiting.has(id)) {
        toast.dismiss(toastIdOf(id));
        notified.current.delete(id);
      }
    }
    for (const id of waitingIds) {
      if (notified.current.has(id) || !panelCollapsed) continue;
      notified.current.add(id);
      toast.warning(getConversationDisplayTitle(id), {
        description: t("ai.approvalWaitingDesc"),
        id: toastIdOf(id),
        duration: Infinity,
        action: {
          label: t("ai.approvalView"),
          onClick: () => {
            expandPanel();
            useAIStore.getState().openSidebarConversationInSidebar(id, { activate: true });
          },
        },
      });
    }
  }, [waitingIds, panelCollapsed, expandPanel, t]);
}
