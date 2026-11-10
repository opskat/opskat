import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useShallow } from "zustand/shallow";
import { getConversationDisplayTitle, selectWaitingApprovalConversationIds, useAIStore } from "@/stores/aiStore";

const toastIdOf = (convId: number) => `ai-approval-${convId}`;

/**
 * AI 面板折叠时，会话进入等待审批状态则弹一条带「查看」的警告提示（每次进入一次），
 * 审批处理完（或面板展开）后撤掉。顶栏数量由 TopBar 自行订阅。
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
      if (notified.current.has(id)) continue;
      notified.current.add(id);
      if (!panelCollapsed) continue;
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
